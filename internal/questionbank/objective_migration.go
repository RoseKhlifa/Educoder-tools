package questionbank

import (
	"database/sql"
	"errors"
)

// migrateObjectiveVerificationLayout accepts both previously used layouts.
// Whole-task verification keeps its published table and API; individual-answer
// evidence uses a separate table. It runs in Open's schema transaction.
func migrateObjectiveVerificationLayout(tx *sql.Tx) error {
	columns, err := objectiveTableColumns(tx, "objective_verifications")
	if err != nil || len(columns) == 0 {
		return err
	}
	if columns["candidate_id"] && !columns["answer_id"] {
		return nil
	}
	if !columns["answer_id"] || columns["candidate_id"] || !columns["verified"] || !columns["evidence_json"] || !columns["created_at"] {
		return errors.New("unrecognized objective verification schema")
	}
	destination, err := objectiveTableColumns(tx, "objective_answer_verifications")
	if err != nil {
		return err
	}
	if len(destination) == 0 {
		_, err = tx.Exec("ALTER TABLE objective_verifications RENAME TO objective_answer_verifications")
		return err
	}
	if !destination["answer_id"] || !destination["verified"] || !destination["evidence_json"] || !destination["created_at"] {
		return errors.New("unrecognized individual objective verification schema")
	}
	// A partially integrated bank can contain both individual-answer tables.
	// Evidence IDs have no dependants; append every row rather than discard
	// records whose local IDs happen to collide with the destination's IDs.
	if _, err = tx.Exec(`INSERT INTO objective_answer_verifications(answer_id,verified,evidence_json,created_at)
		SELECT answer_id,verified,evidence_json,created_at FROM objective_verifications ORDER BY id`); err != nil {
		return err
	}
	_, err = tx.Exec("DROP TABLE objective_verifications")
	return err
}

// migrateObjectiveQuestionContext adds the fail-closed context columns to
// banks created before per-question fingerprints carried shared challenge
// material. Existing rows deliberately remain context_complete=0: their old
// question-only identity cannot prove that the shared title/statement/media
// was read and must not be reused automatically.
func migrateObjectiveQuestionContext(tx *sql.Tx) error {
	columns, err := objectiveTableColumns(tx, "objective_questions")
	if err != nil || len(columns) == 0 {
		return err
	}
	for _, spec := range []struct {
		name string
		ddl  string
	}{
		{"context_json", `ALTER TABLE objective_questions ADD COLUMN context_json TEXT NOT NULL DEFAULT '{}'`},
		{"context_complete", `ALTER TABLE objective_questions ADD COLUMN context_complete INTEGER NOT NULL DEFAULT 0`},
		{"fingerprint_version", `ALTER TABLE objective_questions ADD COLUMN fingerprint_version INTEGER NOT NULL DEFAULT 1`},
	} {
		if columns[spec.name] {
			continue
		}
		if _, err = tx.Exec(spec.ddl); err != nil {
			return err
		}
	}
	return nil
}
func objectiveTableColumns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
