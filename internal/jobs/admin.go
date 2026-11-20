package jobs

import "context"

// AdminRecord is used only by an application handler that has authenticated
// an administrator. It deliberately has no credentials or executable closure.
type AdminRecord struct {
	Job
	OwnerID string `json:"owner_id"`
}

func (m *Manager) AdminList(ctx context.Context, limit int) ([]AdminRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := m.db.QueryContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := []AdminRecord{}
	for rows.Next() {
		s, e := readStored(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, AdminRecord{Job: clone(s.Job), OwnerID: s.owner})
	}
	err = rows.Err()
	// Close the cursor before locking: the single store connection and the
	// manager mutex are taken in the opposite order by a running worker.
	rows.Close()
	if err != nil {
		return nil, err
	}
	live := m.liveJobs("")
	for i, record := range out {
		if fresh, ok := live[record.ID]; ok {
			out[i].Job = fresh
		}
	}
	return out, nil
}

func (m *Manager) AdminGet(ctx context.Context, id string) (AdminRecord, error) {
	s, err := readStored(m.db.QueryRowContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE id=?`, id))
	if err != nil {
		return AdminRecord{}, err
	}
	if job, ok := m.liveJob("", id); ok {
		return AdminRecord{Job: job, OwnerID: s.owner}, nil
	}
	return AdminRecord{Job: clone(s.Job), OwnerID: s.owner}, nil
}

// CancelOwner snapshots active IDs, then uses the regular cooperative cancel
// path. No DB-wide update can conceal a still-running upstream operation.
func (m *Manager) CancelOwner(ctx context.Context, owner string) error {
	m.mu.Lock()
	ids := []string{}
	for id, r := range m.active {
		if r.owner == owner {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		if _, err := m.Cancel(ctx, owner, id); err != nil {
			return err
		}
	}
	return nil
}
