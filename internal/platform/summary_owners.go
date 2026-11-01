package platform

import "context"

// VerifiedJobOwners is an internal aggregation boundary. It reads every linked
// account, without the pagination limit of the admin user directory, and never
// loads names, logins or upstream account numbers. Disabled accounts still
// retain their historical completed work; this method grants no service access.
func (s *Store) VerifiedJobOwners(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM platform_users WHERE upstream_id>0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		owners = append(owners, (User{ID: id}).Owner())
	}
	return owners, rows.Err()
}
