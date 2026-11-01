package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"educoder-cli/internal/answer"
)

// TodaySummary counts work performed and verified by this service, not reusable
// bank contents or job counters. It contains no principal or question details.
type TodaySummary struct {
	Date               string `json:"date"`
	Timezone           string `json:"timezone"`
	CompletedStudents  int64  `json:"completed_students"`
	CompletedQuestions int64  `json:"completed_questions"`
}

// China has used UTC+8 without DST since 1991. Keep the current service-day
// boundary available in minimal containers without depending on OS zone files.
var serviceDayLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// Verification time, rather than a mutable job update time, is the selection
// key. These indexes keep a minute's sidebar poll from scanning old evidence.
const todaySummarySchema = `
CREATE INDEX IF NOT EXISTS objective_verifications_completed_at ON objective_verifications(created_at);
CREATE INDEX IF NOT EXISTS verifications_completed_at ON verifications(created_at);
`

type completionTarget struct {
	Course     string
	HomeworkID int64
	Game       string
}

type completionRun struct {
	Mode   string           `json:"mode"`
	Target completionTarget `json:"target"`
	Result struct {
		RunID       string `json:"run_id"`
		Status      string `json:"status"`
		Kind        string `json:"kind"`
		CandidateID int64  `json:"candidate_id"`
	} `json:"result"`
}

type completionKey struct {
	owner string
	completionTarget
	question string
}

// Solver state can include source files and model repair history. Aggregate
// queries project only the tiny identity/status fields needed for counting.
const completionRunProjection = `json_object(
 'mode', json_extract(r.data_json,'$.mode'),
 'target', json_object('Course',json_extract(r.data_json,'$.target.Course'),
  'HomeworkID',json_extract(r.data_json,'$.target.HomeworkID'),
  'Game',json_extract(r.data_json,'$.target.Game')),
 'result', json_object('run_id',json_extract(r.data_json,'$.result.run_id'),
  'status',json_extract(r.data_json,'$.result.status'),
  'kind',json_extract(r.data_json,'$.result.kind'),
  'candidate_id',json_extract(r.data_json,'$.result.candidate_id')))`

// TodaySummary reads append-only verification timestamps. Only a matching
// submit run that reached passed qualifies; collected historical answers have
// no such run, and generate/selftest/optional/already-passed skips do not qualify.
// A programming level counts as one question; objective levels count their
// individual question positions. Retries and repeated proofs cannot multiply
// one student's target. Legacy evidence missing a run binding is excluded.
// Exams without individual graded correctness evidence are also excluded. The
// caller must supply the hashed owners of all verified linked platform users;
// unlinked CLI/local execution owners never count as students.
func (s *Store) TodaySummary(ctx context.Context, at time.Time, linkedOwners map[string]struct{}) (TodaySummary, error) {
	local := at.In(serviceDayLocation)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, serviceDayLocation)
	end := start.AddDate(0, 0, 1)
	out := TodaySummary{Date: start.Format("2006-01-02"), Timezone: "Asia/Shanghai"}
	completed := make(map[completionKey]struct{})
	students := make(map[string]struct{})
	// Stored timestamps are RFC3339Nano UTC. Second prefixes without a trailing
	// Z preserve ordering at midnight even when the row has fractional seconds.
	lower, upper := start.UTC().Format("2006-01-02T15:04:05"), end.UTC().Format("2006-01-02T15:04:05")
	queries := []string{
		`SELECT 'objective', r.owner, r.id, ` + completionRunProjection + `, v.created_at,
		 c.id, c.candidate_json, snap.snapshot_json, v.score, v.max_score, '', '', ''
		 FROM objective_verifications v
		 JOIN solver_runs r ON r.id=v.run_id AND r.status='passed'
		 JOIN objective_candidates c ON c.id=v.candidate_id
		 JOIN objective_snapshots snap ON snap.snapshot_hash=c.snapshot_hash
		 WHERE v.result='full_score' AND v.created_at>=? AND v.created_at<?`,
		`SELECT 'programming', r.owner, r.id, ` + completionRunProjection + `, v.created_at,
		 c.id, v.evidence_json, '', '', '', c.files_hash, c.revision_hash, c.contract_hash
		 FROM verifications v
		 JOIN solver_runs r ON r.id=json_extract(v.evidence_json,'$.run_id') AND r.status='passed'
		 JOIN solutions c ON c.id=v.solution_id
		 WHERE v.created_at>=? AND v.created_at<?
		 AND json_extract(v.evidence_json,'$.kind')='formal'
		 AND json_extract(v.evidence_json,'$.result')='passed'`,
	}
	// A single read transaction keeps both kinds on the same SQLite snapshot.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return TodaySummary{}, err
	}
	defer tx.Rollback()
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query, lower, upper)
		if err != nil {
			return TodaySummary{}, err
		}
		for rows.Next() {
			var kind, owner, id, rawRun, stamp, evidence, snapshot, score, maxScore, filesHash, revisionHash, contractHash string
			var candidateID int64
			if err := rows.Scan(&kind, &owner, &id, &rawRun, &stamp, &candidateID, &evidence, &snapshot, &score, &maxScore, &filesHash, &revisionHash, &contractHash); err != nil {
				rows.Close()
				return TodaySummary{}, err
			}
			verifiedAt, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || verifiedAt.Before(start) || !verifiedAt.Before(end) || owner == "" {
				continue
			}
			if _, linked := linkedOwners[owner]; !linked {
				continue
			}
			var run completionRun
			if json.Unmarshal([]byte(rawRun), &run) != nil || run.Mode != "submit" || run.Result.Status != "passed" || run.Result.RunID != id || run.Result.CandidateID != candidateID || run.Target.Course == "" || run.Target.HomeworkID <= 0 || run.Target.Game == "" {
				continue
			}
			key := completionKey{owner: owner, completionTarget: run.Target}
			if kind == "objective" {
				var snap answer.ObjectiveSnapshot
				var candidate answer.ObjectiveCandidate
				if run.Result.Kind != "objective" || !objectiveHasFullScore(score, maxScore) || json.Unmarshal([]byte(snapshot), &snap) != nil || json.Unmarshal([]byte(evidence), &candidate) != nil || answer.ValidateObjectiveCandidate(snap, candidate) != nil {
					continue
				}
				for _, q := range snap.Questions {
					key.question = "objective:" + strconv.Itoa(q.Position)
					completed[key] = struct{}{}
				}
			} else {
				var proof Validation
				if json.Unmarshal([]byte(evidence), &proof) != nil || proof.CandidateID != candidateID || proof.RunID != id || proof.Kind != "formal" || proof.Result != "passed" || !proof.Terminal || !proof.Complete || proof.TestCount <= 0 || proof.TestCount != proof.PassedCount || filesHash == "" || proof.FilesHash != filesHash || proof.SavedFilesHash != filesHash || proof.SubmittedFilesHash != filesHash || proof.RevisionHash != revisionHash || proof.ContractHash != contractHash {
					continue
				}
				key.question = "programming"
				completed[key] = struct{}{}
			}
			students[owner] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return TodaySummary{}, err
		}
		if err := rows.Close(); err != nil {
			return TodaySummary{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TodaySummary{}, err
	}
	out.CompletedStudents, out.CompletedQuestions = int64(len(students)), int64(len(completed))
	return out, nil
}
