package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

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

// New platform owners use the stable local identity, while historical callers
// use a login digest. Keep both namespaces exact; collection evidence below
// must continue hashing this raw owner exactly once like solver_runs.
var collectionOwner = regexp.MustCompile(`^(?:[a-f0-9]{64}|platform:[a-f0-9]{32})$`)

// RememberCollectionTarget is invoked only after a normal authenticated task
// read, never from browser-supplied challenge IDs. Owner is server-derived.
func (s *Store) RememberCollectionTarget(ctx context.Context, owner string, t CollectionTarget) error {
	if !collectionOwner.MatchString(owner) || !collectionID.MatchString(t.Course) || !collectionID.MatchString(t.Game) || t.HomeworkID < 1 || t.ChallengeID < 1 || !collectionID.MatchString(t.Shixun) {
		return errors.New("invalid collection target")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO collection_targets(owner,course,homework_id,game,challenge_id,shixun) VALUES(?,?,?,?,?,?) ON CONFLICT(owner,course,homework_id,game) DO UPDATE SET challenge_id=excluded.challenge_id,shixun=excluded.shixun`, owner, t.Course, t.HomeworkID, t.Game, t.ChallengeID, t.Shixun)
	return err
}

// CollectionTargets preserves the legacy query API. A zero limit means all
// targets; production collection uses the narrower owner/homework query below.
func (s *Store) CollectionTargets(ctx context.Context, owner string, limit int) ([]CollectionTarget, error) {
	if !collectionOwner.MatchString(owner) || limit < 0 {
		return nil, errors.New("invalid target query")
	}
	return s.collectionTargets(ctx, owner, "", 0, limit)
}

// CollectionTargetsForHomework does not truncate another course's or a newer
// run's evidence into the first global page. Both sources remain owner-bound.
func (s *Store) CollectionTargetsForHomework(ctx context.Context, owner, course string, homeworkID int64) ([]CollectionTarget, error) {
	if !collectionOwner.MatchString(owner) || !collectionID.MatchString(course) || homeworkID < 1 {
		return nil, errors.New("invalid target query")
	}
	return s.collectionTargets(ctx, owner, course, homeworkID, 0)
}

func (s *Store) collectionTargets(ctx context.Context, owner, course string, homeworkID int64, limit int) ([]CollectionTarget, error) {
	query := `SELECT course,homework_id,game,challenge_id,shixun FROM collection_targets WHERE owner=?`
	args := []any{owner}
	if course != "" {
		query += ` AND course=? AND homework_id=?`
		args = append(args, course, homeworkID)
	}
	query += ` ORDER BY course,homework_id,game`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []CollectionTarget
	seen := map[string]bool{}
	appendTarget := func(t CollectionTarget) {
		key := t.Course + ":" + strconv.FormatInt(t.HomeworkID, 10) + ":" + t.Game
		if !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	for rows.Next() {
		var t CollectionTarget
		if err = rows.Scan(&t.Course, &t.HomeworkID, &t.Game, &t.ChallengeID, &t.Shixun); err != nil {
			break
		}
		appendTarget(t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || (limit > 0 && len(out) >= limit) {
		return out, err
	}
	// Legacy solver.Target had CamelCase JSON fields; newer remembered targets
	// use snake_case. Scope at the database before decoding without a global
	// newest-500 window. The full Go identity check below is still decisive.
	query = `SELECT data_json FROM solver_runs WHERE owner=? AND status='passed'`
	args = []any{problem.HashContent(owner)}
	if course != "" {
		query += ` AND CASE WHEN json_valid(data_json) THEN COALESCE(json_extract(data_json,'$.target.course'),json_extract(data_json,'$.target.Course')) END=?
		AND CASE WHEN json_valid(data_json) THEN COALESCE(json_extract(data_json,'$.target.homework_id'),json_extract(data_json,'$.target.HomeworkID')) END=?`
		args = append(args, course, homeworkID)
	}
	query += ` ORDER BY updated_at DESC,id`
	rows, err = s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		t, ok := collectionRunTarget(raw)
		if !ok || (course != "" && (t.Course != course || t.HomeworkID != homeworkID)) {
			continue
		}
		appendTarget(t)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func collectionRunTarget(raw string) (CollectionTarget, bool) {
	var v struct {
		Target CollectionTarget `json:"target"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return CollectionTarget{}, false
	}
	// encoding/json matches Course/Game case-insensitively, but HomeworkID
	// does not match the snake_case tag. Preserve exact legacy field support.
	if v.Target.HomeworkID == 0 {
		var legacy struct {
			Target struct{ HomeworkID int64 } `json:"target"`
		}
		if json.Unmarshal([]byte(raw), &legacy) == nil {
			v.Target.HomeworkID = legacy.Target.HomeworkID
		}
	}
	return v.Target, collectionID.MatchString(v.Target.Game) && collectionID.MatchString(v.Target.Course) && v.Target.HomeworkID > 0
}

// HasCollectionCodeEvidence requires an existing formal submission from THIS
// owner and target, not merely a similar solution from another user's cache.
// The observed upstream GET does not expose a submitted-source revision, so
// external history without this bridge remains explicitly uncollectable.
func (s *Store) HasCollectionCodeEvidence(ctx context.Context, owner string, t CollectionTarget, ref Ref, hash string) (bool, error) {
	if !collectionOwner.MatchString(owner) || !objectiveSHA256Pattern.MatchString(hash) {
		return false, errors.New("invalid collection evidence query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,data_json FROM solver_runs WHERE owner=? AND status='passed'
	AND CASE WHEN json_valid(data_json) THEN COALESCE(json_extract(data_json,'$.target.course'),json_extract(data_json,'$.target.Course')) END=?
	AND CASE WHEN json_valid(data_json) THEN COALESCE(json_extract(data_json,'$.target.homework_id'),json_extract(data_json,'$.target.HomeworkID')) END=?
	AND CASE WHEN json_valid(data_json) THEN COALESCE(json_extract(data_json,'$.target.game'),json_extract(data_json,'$.target.Game')) END=?
	ORDER BY updated_at DESC,id`, problem.HashContent(owner), t.Course, t.HomeworkID, t.Game)
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
		target, validTarget := collectionRunTarget(raw)
		if json.Unmarshal([]byte(raw), &state) == nil && validTarget && target.Course == t.Course && target.HomeworkID == t.HomeworkID && target.Game == t.Game && state.Ref == ref && state.Result.CodeHash == hash && state.Result.CandidateID > 0 {
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
