package educoder

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// GraphicMutationStage records whether a formal build could have reached the
// platform. Preparation/saves are not submissions. Unknown build delivery must
// never be replayed by a caller treating an ordinary pre-write failure as safe.
type GraphicMutationStage string

const (
	GraphicStagePrewrite          GraphicMutationStage = "prewrite"
	GraphicStageSaveUnconfirmed   GraphicMutationStage = "save_unconfirmed"
	GraphicStageSavedNotSubmitted GraphicMutationStage = "saved_not_submitted"
	GraphicStageBuildSent         GraphicMutationStage = "build_sent_unknown"
	GraphicStageBuildAccepted     GraphicMutationStage = "build_accepted"
	GraphicStageCompleted         GraphicMutationStage = "completed"
)

// GraphicMutationError preserves a typed mutation boundary without exposing
// upstream error bodies, SSH addresses, keys, passwords or signed links.
type GraphicMutationError struct {
	Stage GraphicMutationStage
	Cause error
}

func (e *GraphicMutationError) Error() string {
	switch e.Stage {
	case GraphicStageSavedNotSubmitted:
		return "图形文件已保存但未提交评测"
	case GraphicStageSaveUnconfirmed:
		return "图形文件保存结果尚未确认，未发起评测"
	case GraphicStageBuildSent, GraphicStageBuildAccepted:
		return "图形评测已发起，结果尚未确认；仅可复核，不重放构建"
	default:
		return "图形操作在提交之前停止"
	}
}
func (e *GraphicMutationError) Unwrap() error { return e.Cause }

// VNC environments are limited per upstream account, not per game/client.
// Reference counts remove idle locks instead of retaining account identities.
var graphicAccountLocks = struct {
	sync.Mutex
	entries map[graphicAccountKey]*graphicAccountLock
}{entries: map[graphicAccountKey]*graphicAccountLock{}}

type graphicAccountKey struct {
	upstream string
	user     int64
}
type graphicAccountLock struct {
	token chan struct{}
	refs  int
}

func (p *GraphicProtocol) lockAccount(ctx context.Context, t *Task) (func(), error) {
	if p == nil || p.Client == nil || t == nil || t.User.UserID <= 0 {
		return nil, errors.New("图形账号锁缺少已核实身份")
	}
	key := graphicAccountKey{strings.TrimRight(p.Client.BaseURL, "/"), t.User.UserID}
	graphicAccountLocks.Lock()
	lock := graphicAccountLocks.entries[key]
	if lock == nil {
		lock = &graphicAccountLock{token: make(chan struct{}, 1)}
		graphicAccountLocks.entries[key] = lock
	}
	lock.refs++
	graphicAccountLocks.Unlock()
	drop := func() {
		graphicAccountLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(graphicAccountLocks.entries, key)
		}
		graphicAccountLocks.Unlock()
	}
	select {
	case lock.token <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-lock.token; drop() }) }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// acquireEnvironment serializes graphical and MySQL source-bound operations.
// Only an explicit is_start=false observation followed by our accepted start
// establishes ownership. Existing/unknown environments are used, never deleted.
func (p *GraphicProtocol) acquireEnvironment(ctx context.Context, t *Task, env ProgrammingEnvironment) (session GraphicSession, cleanup func(*GraphicSubmitResult), err error) {
	unlock, err := p.lockAccount(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	owned := false
	session, err = p.openEnvironment(ctx, t, env, &owned)
	cleanup = func(result *GraphicSubmitResult) {
		defer unlock()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		var failed bool
		if session != nil {
			failed = session.Close() != nil
		}
		if owned {
			releaseErr := p.Client.ReleaseGraphicEnvironment(cleanupCtx, t, env.EnvironmentID)
			if result != nil {
				result.EnvironmentReleased = releaseErr == nil
			}
			failed = failed || releaseErr != nil
		}
		if failed && result != nil {
			result.CleanupError = "图形会话清理未完全确认；正式评测结果保留"
		}
	}
	if err != nil {
		cleanup(nil)
		return nil, nil, err
	}
	return session, cleanup, nil
}
