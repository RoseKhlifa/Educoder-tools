package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

type execution struct {
	stored
	exec            Executor
	cancel          context.CancelFunc
	cancelRequested bool
}

type Manager struct {
	mu         sync.Mutex
	cfg        Config
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	active     map[string]*execution
	owners     map[string]int
	queue      []string
	wake       chan struct{}
	closed     bool
	wg         sync.WaitGroup
	done       chan struct{}
	closeOnce  sync.Once
	lastPruned time.Time
}

func Open(ctx context.Context, cfg Config) (*Manager, error) {
	var err error
	cfg, err = cfg.defaults()
	if err != nil {
		return nil, err
	}
	db, err := openStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = recoverStore(ctx, db, time.Now().UTC(), cfg.Retention); err != nil {
		_ = db.Close()
		return nil, err
	}
	runctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, db: db, ctx: runctx, cancel: cancel, active: make(map[string]*execution), owners: make(map[string]int), wake: make(chan struct{}), done: make(chan struct{}), lastPruned: time.Now()}
	for i := 0; i < cfg.Workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return m, nil
}

// New is the context-free convenience constructor. Applications that already
// have startup cancellation should use Open with that context instead.
func New(cfg Config) (*Manager, error) { return Open(context.Background(), cfg) }

func (m *Manager) notifyLocked() { close(m.wake); m.wake = make(chan struct{}) }

func (m *Manager) Submit(ctx context.Context, owner string, req Request, exec Executor) (Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, false, err
	}
	if owner == "" || len(owner) > 256 || exec == nil {
		return Job{}, false, fail("invalid_request", "Owner and executor are required")
	}
	req, payload, target, err := normalize(req)
	if err != nil {
		return Job{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Job{}, false, fail("closed", "Job service is shutting down")
	}
	if req.IdempotencyKey != "" {
		s, e := readStored(m.db.QueryRowContext(ctx, `SELECT j.owner,j.payload,j.target,j.mutating,j.data FROM async_jobs j JOIN async_job_keys k ON k.job_id=j.id WHERE k.owner=? AND k.key=?`, owner, req.IdempotencyKey))
		if e == nil {
			if s.payload != payload {
				return Job{}, false, fail("idempotency_conflict", "Idempotency key was already used for a different request")
			}
			return clone(s.Job), true, nil
		}
		if !IsCode(e, "not_found") {
			return Job{}, false, e
		}
	}
	// A target/mode has at most one queued or running execution per owner.
	for _, r := range m.active {
		if r.owner == owner && r.target == target {
			if req.IdempotencyKey != "" {
				if _, err = m.db.Exec(`INSERT INTO async_job_keys(owner,key,payload,job_id) VALUES(?,?,?,?)`, owner, req.IdempotencyKey, payload, r.ID); err != nil {
					return Job{}, false, err
				}
			}
			return clone(r.Job), true, nil
		}
	}
	pending := 0
	for _, r := range m.active {
		if r.owner == owner {
			pending++
		}
	}
	if pending >= m.cfg.PerOwnerPending {
		return Job{}, false, fail("owner_limit", "This account already has the maximum pending jobs")
	}
	if len(m.queue) >= m.cfg.QueueSize {
		return Job{}, false, fail("queue_full", "The job queue is full; try again later")
	}
	var idbytes [16]byte
	if _, err = rand.Read(idbytes[:]); err != nil {
		return Job{}, false, err
	}
	now := time.Now().UTC()
	kind := "programming"
	if req.Game == "" {
		kind = "homework"
	}
	r := &execution{stored: stored{Job: Job{ID: hex.EncodeToString(idbytes[:]), Status: Queued, Phase: Queued, Kind: kind, Request: req, CreatedAt: now, UpdatedAt: now, Message: "Waiting for an available worker"}, owner: owner, payload: payload, target: target}, exec: exec}
	r.Events = []Event{{Phase: Queued, Message: r.Message, At: now}}
	if err = insertStore(m.db, &r.stored); err != nil {
		return Job{}, false, err
	}
	m.active[r.ID] = r
	m.queue = append(m.queue, r.ID)
	m.notifyLocked()
	if time.Since(m.lastPruned) > time.Hour {
		_, _ = m.db.Exec(`DELETE FROM async_jobs WHERE status NOT IN ('queued','running') AND updated_at < ?`, now.Add(-m.cfg.Retention).UnixNano())
		_, _ = m.db.Exec(`DELETE FROM async_job_keys WHERE job_id NOT IN (SELECT id FROM async_jobs)`)
		m.lastPruned = now
	}
	return clone(r.Job), false, nil
}

func (m *Manager) Get(ctx context.Context, owner, id string) (Job, error) {
	if owner == "" {
		return Job{}, fail("not_found", "Job not found")
	}
	s, err := readStored(m.db.QueryRowContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE id=? AND owner=?`, id, owner))
	if err != nil {
		return Job{}, err
	}
	return clone(s.Job), nil
}

func (m *Manager) List(ctx context.Context, owner string, limit int) ([]Job, error) {
	if owner == "" {
		return nil, fail("not_found", "Job not found")
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := m.db.QueryContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE owner=? ORDER BY created_at DESC LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		s, e := readStored(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, clone(s.Job))
	}
	return out, rows.Err()
}

func (m *Manager) Cancel(ctx context.Context, owner, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.active[id]
	if !ok || r.owner != owner {
		s, err := readStored(m.db.QueryRowContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE id=? AND owner=?`, id, owner))
		if err != nil {
			return Job{}, err
		}
		return clone(s.Job), nil
	}
	r.cancelRequested = true
	if r.Status == Queued {
		m.removeQueuedLocked(id)
		m.terminalLocked(r, Canceled, "canceled", "Job canceled before execution", nil)
		m.notifyLocked()
	} else {
		r.Message = "Cancellation requested"
		r.UpdatedAt = time.Now().UTC()
		if r.mutating {
			r.Message = "Cancellation requested after submission began; upstream work may continue"
		}
		if err := writeStore(m.db, &r.stored, false); err != nil {
			return Job{}, err
		}
		if r.cancel != nil {
			r.cancel()
		}
	}
	return clone(r.Job), nil
}

func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := 0
	for _, r := range m.active {
		if r.Status == Running {
			running++
		}
	}
	return Stats{Workers: m.cfg.Workers, Running: running, Queued: len(m.queue), QueueCapacity: m.cfg.QueueSize, PerOwnerPending: m.cfg.PerOwnerPending, Accepting: !m.closed}
}

func (m *Manager) removeQueuedLocked(id string) {
	for i, q := range m.queue {
		if q == id {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

func (m *Manager) claim() (*execution, context.Context, <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, nil
	}
	for i := 0; i < len(m.queue); {
		id := m.queue[i]
		r := m.active[id]
		if r == nil {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			continue
		}
		if m.owners[r.owner] > 0 {
			i++
			continue
		}
		m.queue = append(m.queue[:i], m.queue[i+1:]...)
		now := time.Now().UTC()
		r.Status = Running
		r.Phase = "starting"
		r.StartedAt = &now
		r.UpdatedAt = now
		r.Message = "Job started"
		if err := writeStore(m.db, &r.stored, false); err != nil {
			m.terminalLocked(r, Failed, "storage_error", "Job state could not be saved", nil)
			m.notifyLocked()
			continue
		}
		ctx, cancel := context.WithTimeout(m.ctx, m.cfg.Timeout)
		r.cancel = cancel
		m.owners[r.owner]++
		return r, ctx, nil
	}
	return nil, nil, m.wake
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		r, ctx, wake := m.claim()
		if r == nil {
			if wake == nil {
				return
			}
			select {
			case <-wake:
				continue
			case <-m.ctx.Done():
				return
			}
		}
		out, err := run(ctx, r.exec, r.Request, func(e Event) { m.report(r, e) })
		ctxerr := ctx.Err()
		r.cancel()
		m.mu.Lock()
		if r.cancelRequested && ctxerr == nil {
			ctxerr = context.Canceled
		}
		status := out.Status
		if status == "" {
			status = Succeeded
		}
		if status != Succeeded && status != Failed && status != Canceled && status != Uncertain {
			status = Failed
			out.ErrorCode = "invalid_outcome"
			out.Message = "Executor returned an invalid outcome"
		}
		result, reserr := publicResult(out.Result, m.cfg.MaxResultBytes)
		if reserr != nil {
			status = Failed
			out.ErrorCode = "invalid_result"
			out.Message = reserr.Error()
			result = nil
		}
		if err != nil {
			status = Failed
			out.ErrorCode = "execution_failed"
			out.Message = publicText(err.Error(), "Job execution failed")
		}
		if ctxerr != nil {
			status = Canceled
			out.ErrorCode = "canceled"
			out.Message = "Job canceled"
			if errors.Is(ctxerr, context.DeadlineExceeded) {
				status = Failed
				out.ErrorCode = "timeout"
				out.Message = "Job exceeded its execution time limit"
			}
		}
		if r.mutating && (err != nil || ctxerr != nil || reserr != nil || status == Canceled) {
			status = Uncertain
			out.ErrorCode = "submission_uncertain"
			out.Message = "Submission may have reached the upstream service; check its result before retrying"
		}
		m.owners[r.owner]--
		if m.owners[r.owner] == 0 {
			delete(m.owners, r.owner)
		}
		m.terminalLocked(r, status, publicLabel(out.ErrorCode, ""), publicText(out.Message, defaultMessage(status)), result)
		m.notifyLocked()
		m.mu.Unlock()
	}
}

func run(ctx context.Context, exec Executor, req Request, report func(Event)) (out Outcome, err error) {
	defer func() {
		if recover() != nil {
			out = Outcome{}
			err = fmt.Errorf("Job executor stopped unexpectedly")
		}
	}()
	return exec(ctx, req, report)
}

func defaultMessage(status string) string {
	switch status {
	case Succeeded:
		return "Job completed"
	case Canceled:
		return "Job canceled"
	case Uncertain:
		return "Upstream result needs verification"
	default:
		return "Job failed"
	}
}

func (m *Manager) report(r *execution, e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[r.ID] != r || r.Status != Running {
		return
	}
	e.Phase = publicLabel(e.Phase, "working")
	e.Kind = publicLabel(e.Kind, "")
	e.Message = publicText(e.Message, "Job is working")
	e.At = time.Now().UTC()
	r.Phase = e.Phase
	if e.Kind != "" {
		r.Kind = e.Kind
	}
	r.Message = e.Message
	r.UpdatedAt = e.At
	r.mutating = r.mutating || mutatingPhase(e.Phase)
	r.Events = append(r.Events, e)
	if len(r.Events) > m.cfg.EventLimit {
		r.Events = append([]Event(nil), r.Events[len(r.Events)-m.cfg.EventLimit:]...)
	}
	if err := writeStore(m.db, &r.stored, false); err != nil {
		r.cancelRequested = true
		if r.cancel != nil {
			r.cancel()
		}
	}
}

func (m *Manager) terminalLocked(r *execution, status, code, message string, result []byte) {
	now := time.Now().UTC()
	r.Status = status
	r.UpdatedAt = now
	r.FinishedAt = &now
	r.ErrorCode = code
	r.Message = message
	r.Result = result
	r.Events = append(r.Events, Event{Phase: status, Message: message, At: now})
	if len(r.Events) > m.cfg.EventLimit {
		r.Events = append([]Event(nil), r.Events[len(r.Events)-m.cfg.EventLimit:]...)
	}
	_ = writeStore(m.db, &r.stored, false)
	delete(m.active, r.ID)
	r.exec = nil
	r.cancel = nil
}

// Close stops admission and cancels queued/running work. Executors must honor
// their context, as Go cannot forcibly terminate an arbitrary goroutine.
// A deadline bounds the caller's wait without closing a database still in use.
func (m *Manager) Close(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		for _, id := range append([]string(nil), m.queue...) {
			r := m.active[id]
			if r != nil {
				m.terminalLocked(r, Canceled, "service_shutdown", "Service shut down before execution", nil)
			}
		}
		m.queue = nil
		for _, r := range m.active {
			r.cancelRequested = true
			if r.cancel != nil {
				r.cancel()
			}
		}
		m.cancel()
		m.notifyLocked()
		m.mu.Unlock()
		go func() { m.wg.Wait(); _ = m.db.Close(); close(m.done) }()
	})
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
