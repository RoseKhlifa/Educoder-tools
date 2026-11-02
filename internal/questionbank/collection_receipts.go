package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"educoder-cli/internal/problem"
)

// Collection receipts contain only a server-derived account key, task IDs,
// hashes and references to already verified bank records. A receipt is a local
// checkpoint, never independent evidence that an answer is correct.
const collectionReceiptSchema = `CREATE TABLE IF NOT EXISTS collection_receipts (
 owner TEXT NOT NULL, course TEXT NOT NULL, homework_id INTEGER NOT NULL,
 challenge_id INTEGER NOT NULL, fingerprint TEXT NOT NULL,
 verified_at INTEGER NOT NULL, questions INTEGER NOT NULL,
 objective_answer_ids TEXT NOT NULL, code_candidate_id INTEGER NOT NULL,
 PRIMARY KEY(owner,course,homework_id,challenge_id));`

type CollectionReceipt struct {
	Fingerprint        string
	VerifiedAt         time.Time
	Questions          int
	ObjectiveAnswerIDs []int64
	CodeCandidateID    int64
}

func validCollectionReceiptScope(owner, course string, homeworkID int64) bool {
	return collectionOwner.MatchString(owner) && collectionID.MatchString(course) && homeworkID > 0
}

func validCollectionReceipt(r CollectionReceipt) bool {
	stamp := r.VerifiedAt.UnixNano()
	if !objectiveSHA256Pattern.MatchString(r.Fingerprint) || r.Questions <= 0 || r.VerifiedAt.IsZero() || stamp <= 0 || !time.Unix(0, stamp).Equal(r.VerifiedAt) {
		return false
	}
	if r.CodeCandidateID > 0 {
		return r.Questions == 1 && len(r.ObjectiveAnswerIDs) == 0
	}
	if r.CodeCandidateID < 0 || len(r.ObjectiveAnswerIDs) != r.Questions {
		return false
	}
	for _, id := range r.ObjectiveAnswerIDs {
		if id <= 0 {
			return false
		}
	}
	return true
}

// CollectionReceipts returns only checkpoints whose referenced answers still
// have reusable bank evidence. Missing or invalidated records are omitted so
// an interrupted collection or a later bank correction cannot hide questions.
func (s *Store) CollectionReceipts(ctx context.Context, owner, course string, homeworkID int64) (map[int64]CollectionReceipt, error) {
	if !validCollectionReceiptScope(owner, course, homeworkID) {
		return nil, errors.New("invalid collection receipt query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT challenge_id,fingerprint,verified_at,questions,objective_answer_ids,code_candidate_id
 FROM collection_receipts WHERE owner=? AND course=? AND homework_id=?`, owner, course, homeworkID)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]CollectionReceipt)
	for rows.Next() {
		var challengeID, stamp int64
		var raw string
		var r CollectionReceipt
		if err = rows.Scan(&challengeID, &r.Fingerprint, &stamp, &r.Questions, &raw, &r.CodeCandidateID); err != nil {
			break
		}
		r.VerifiedAt = time.Unix(0, stamp).UTC()
		if challengeID > 0 && json.Unmarshal([]byte(raw), &r.ObjectiveAnswerIDs) == nil && validCollectionReceipt(r) {
			out[challengeID] = r
		}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// Release the receipt query before inspecting bank evidence: the bank uses
	// one SQLite connection to keep its connection-local PRAGMAs in effect.
	for challengeID, r := range out {
		valid, err := s.collectionReceiptEvidence(ctx, r)
		if err != nil {
			return nil, err
		}
		if !valid {
			delete(out, challengeID)
		}
	}
	return out, nil
}

// PutCollectionReceipt is called only after authenticated collection has
// matched the current target fingerprint and persisted every verified answer.
// Partial collections must not write a receipt.
func (s *Store) PutCollectionReceipt(ctx context.Context, owner, course string, homeworkID, challengeID int64, r CollectionReceipt) error {
	if !validCollectionReceiptScope(owner, course, homeworkID) || challengeID <= 0 || !validCollectionReceipt(r) {
		return errors.New("invalid collection receipt")
	}
	valid, err := s.collectionReceiptEvidence(ctx, r)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("collection receipt has no reusable answer evidence")
	}
	ids := r.ObjectiveAnswerIDs
	if ids == nil {
		ids = []int64{}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO collection_receipts(owner,course,homework_id,challenge_id,fingerprint,verified_at,questions,objective_answer_ids,code_candidate_id)
 VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(owner,course,homework_id,challenge_id) DO UPDATE SET
 fingerprint=excluded.fingerprint,verified_at=excluded.verified_at,questions=excluded.questions,
 objective_answer_ids=excluded.objective_answer_ids,code_candidate_id=excluded.code_candidate_id`,
		owner, course, homeworkID, challengeID, r.Fingerprint, r.VerifiedAt.UnixNano(), r.Questions, marshal(ids), r.CodeCandidateID)
	return err
}

func (s *Store) DeleteCollectionReceipt(ctx context.Context, owner, course string, homeworkID, challengeID int64) error {
	if !validCollectionReceiptScope(owner, course, homeworkID) || challengeID <= 0 {
		return errors.New("invalid collection receipt target")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM collection_receipts WHERE owner=? AND course=? AND homework_id=? AND challenge_id=?`, owner, course, homeworkID, challengeID)
	return err
}

func (s *Store) collectionReceiptEvidence(ctx context.Context, r CollectionReceipt) (bool, error) {
	if r.CodeCandidateID > 0 {
		c, err := s.Candidate(ctx, r.CodeCandidateID)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		hash, err := problem.HashFiles(c.Files)
		return err == nil && hash == c.FilesHash && c.Status == "upstream_verified", nil
	}
	for _, id := range r.ObjectiveAnswerIDs {
		var reusable bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM objective_answers a
 JOIN objective_questions q ON q.question_key=a.question_key AND q.resource_complete=1
 WHERE a.id=? AND EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1))`, id).Scan(&reusable)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil || !reusable {
			return false, err
		}
	}
	return len(r.ObjectiveAnswerIDs) > 0, nil
}
