package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"educoder-cli/internal/problem"
)

const collectionSchema = `CREATE TABLE IF NOT EXISTS collection_targets (
 owner TEXT NOT NULL, course TEXT NOT NULL, homework_id INTEGER NOT NULL,
 game TEXT NOT NULL, challenge_id INTEGER NOT NULL, shixun TEXT NOT NULL,
 PRIMARY KEY(owner,course,homework_id,game));`

type CollectionTarget struct {
	Course      string `json:"course"`
	HomeworkID  int64  `json:"homework_id"`
	Game        string `json:"game"`
	ChallengeID int64  `json:"challenge_id"`
	Shixun      string `json:"shixun"`
}

var collectionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// RememberCollectionTarget is invoked only after a normal authenticated task
// read, never from browser-supplied challenge IDs. Owner is already hashed.
func (s *Store) RememberCollectionTarget(ctx context.Context, owner string, t CollectionTarget) error {
	if !objectiveSHA256Pattern.MatchString(owner) || !collectionID.MatchString(t.Course) || !collectionID.MatchString(t.Game) || t.HomeworkID < 1 || t.ChallengeID < 1 || !collectionID.MatchString(t.Shixun) {
		return errors.New("invalid collection target")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO collection_targets(owner,course,homework_id,game,challenge_id,shixun) VALUES(?,?,?,?,?,?) ON CONFLICT(owner,course,homework_id,game) DO UPDATE SET challenge_id=excluded.challenge_id,shixun=excluded.shixun`, owner, t.Course, t.HomeworkID, t.Game, t.ChallengeID, t.Shixun)
	return err
}

func (s *Store) CollectionTargets(ctx context.Context, owner string, limit int) ([]CollectionTarget, error) {
	if !objectiveSHA256Pattern.MatchString(owner) || limit < 1 || limit > 500 {
		return nil, errors.New("invalid target query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT course,homework_id,game,challenge_id,shixun FROM collection_targets WHERE owner=? ORDER BY course,homework_id,game LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	var out []CollectionTarget
	for rows.Next() {
		var t CollectionTarget
		if err = rows.Scan(&t.Course, &t.HomeworkID, &t.Game, &t.ChallengeID, &t.Shixun); err != nil {
			break
		}
		out = append(out, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Older versions persisted successful task targets in owner-bound runs.
	// These legacy seeds have no challenge ID and require an entirely full-score
	// homework before the collector may read their details.
	rows, err = s.db.QueryContext(ctx, `SELECT data_json FROM solver_runs WHERE owner=? AND status='passed' ORDER BY updated_at DESC LIMIT ?`, problem.HashContent(owner), limit-len(out))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v struct {
			Target CollectionTarget `json:"target"`
		}
		if json.Unmarshal([]byte(raw), &v) == nil && collectionID.MatchString(v.Target.Game) && collectionID.MatchString(v.Target.Course) && v.Target.HomeworkID > 0 {
			out = append(out, v.Target)
		}
	}
	return out, rows.Err()
}

// HasCollectionCodeEvidence requires an existing formal submission from THIS
// owner and target, not merely a similar solution from another user's cache.
// The observed upstream GET does not expose a submitted-source revision, so
// external history without this bridge remains explicitly uncollectable.
func (s *Store) HasCollectionCodeEvidence(ctx context.Context, owner string, t CollectionTarget, ref Ref, hash string) (bool, error) {
	if !objectiveSHA256Pattern.MatchString(owner) || !objectiveSHA256Pattern.MatchString(hash) {
		return false, errors.New("invalid collection evidence query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,data_json FROM solver_runs WHERE owner=? AND status='passed' ORDER BY updated_at DESC LIMIT 500`, problem.HashContent(owner))
	if err != nil {
		return false, err
	}
	type saved struct {
		id        string
		candidate int64
	}
	var matches []saved
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			break
		}
		var state struct {
			Target CollectionTarget `json:"target"`
			Ref    Ref              `json:"snapshot_ref"`
			Result struct {
				CandidateID int64  `json:"candidate_id"`
				CodeHash    string `json:"code_hash"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(raw), &state) == nil && state.Target.Course == t.Course && state.Target.HomeworkID == t.HomeworkID && state.Target.Game == t.Game && state.Ref == ref && state.Result.CodeHash == hash && state.Result.CandidateID > 0 {
			matches = append(matches, saved{id, state.Result.CandidateID})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, m := range matches {
		c, err := s.Candidate(ctx, m.candidate)
		if err != nil {
			return false, err
		}
		actual, e := problem.HashFiles(c.Files)
		if e != nil || actual != hash || c.Ref != ref || c.FilesHash != hash || c.Status != "upstream_verified" {
			continue
		}
		for _, v := range c.Validations {
			if v.RunID == m.id && v.Kind == "formal" && v.Result == "passed" && v.FilesHash == hash && v.SavedFilesHash == hash && v.SubmittedFilesHash == hash && v.Terminal && v.Complete && v.TestCount > 0 && v.TestCount == v.PassedCount && v.RevisionHash == ref.RevisionHash && v.ContractHash == ref.ContractHash {
				return true, nil
			}
		}
	}
	return false, nil
}
