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
	defer rows.Close()
	out := []AdminRecord{}
	for rows.Next() {
		s, e := readStored(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, AdminRecord{Job: clone(s.Job), OwnerID: s.owner})
	}
	return out, rows.Err()
}

func (m *Manager) AdminGet(ctx context.Context, id string) (AdminRecord, error) {
	s, err := readStored(m.db.QueryRowContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE id=?`, id))
	if err != nil {
		return AdminRecord{}, err
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
