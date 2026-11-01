package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidJobLabel indicates malformed display metadata. It deliberately does
// not include the input value, which may have come from a remote response.
var ErrInvalidJobLabel = errors.New("invalid job display label")

const maxJobLabelBatch = 1000

// CourseLabel and HomeworkLabel are display-only metadata obtained from verified
// upstream course/homework responses. They never authorize access, bind an owner,
// or change a platform user's identity or role. Callers must select these fields
// explicitly; response bodies, resource URLs and request secrets do not belong here.
type CourseLabel struct {
	Identifier string
	Name       string
}

type HomeworkLabel struct {
	ID   int64
	Name string
}

func validCourseLabelIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if c != '_' && c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func normalizedJobLabelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 512 {
		return "", ErrInvalidJobLabel
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return "", ErrInvalidJobLabel
		}
	}
	// These fields contain names, never signed resource URLs. Reject an obvious
	// accidental URL without rejecting titles such as "HTTP与网络编程".
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "file:") {
		return "", ErrInvalidJobLabel
	}
	return name, nil
}

// RememberCourses persists only a bounded collection of readable course names.
// Validation precedes the transaction, so one invalid item cannot partially
// update the directory. Repeated identical names do not update existing rows.
func (s *Store) RememberCourses(ctx context.Context, labels []CourseLabel) error {
	return s.rememberCourses(ctx, labels, true)
}

// RememberMissingCourses fills labels missing from the directory using historic
// job metadata. Unlike verified current reads, historical names never replace a
// label that is already known.
func (s *Store) RememberMissingCourses(ctx context.Context, labels []CourseLabel) error {
	return s.rememberCourses(ctx, labels, false)
}

func (s *Store) rememberCourses(ctx context.Context, labels []CourseLabel, overwrite bool) error {
	if len(labels) > maxJobLabelBatch {
		return fmt.Errorf("%w: course batch limit", ErrInvalidJobLabel)
	}
	normalized := make([]CourseLabel, len(labels))
	for i, label := range labels {
		if !validCourseLabelIdentifier(label.Identifier) {
			return fmt.Errorf("%w: course identifier", ErrInvalidJobLabel)
		}
		name, err := normalizedJobLabelName(label.Name)
		if err != nil {
			return fmt.Errorf("%w: course name", err)
		}
		normalized[i] = CourseLabel{Identifier: label.Identifier, Name: name}
	}
	if len(normalized) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	statement := `INSERT INTO job_course_labels(course_identifier,name,updated_at) VALUES(?,?,?)
ON CONFLICT(course_identifier) DO NOTHING`
	if overwrite {
		statement = `INSERT INTO job_course_labels(course_identifier,name,updated_at) VALUES(?,?,?)
ON CONFLICT(course_identifier) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at
WHERE job_course_labels.name<>excluded.name`
	}
	for _, label := range normalized {
		_, err = tx.ExecContext(ctx, statement, label.Identifier, label.Name, stamp)
		if err != nil {
			return fmt.Errorf("remember course display label: %w", err)
		}
	}
	return tx.Commit()
}

// RememberHomeworks uses the course identifier as part of the key. A name from
// another classroom can never replace a homework's display label in this one.
func (s *Store) RememberHomeworks(ctx context.Context, course string, labels []HomeworkLabel) error {
	return s.rememberHomeworks(ctx, course, labels, true)
}

// RememberMissingHomeworks fills unknown names from historic job metadata while
// preserving newer names already obtained from authenticated upstream reads.
func (s *Store) RememberMissingHomeworks(ctx context.Context, course string, labels []HomeworkLabel) error {
	return s.rememberHomeworks(ctx, course, labels, false)
}

func (s *Store) rememberHomeworks(ctx context.Context, course string, labels []HomeworkLabel, overwrite bool) error {
	if !validCourseLabelIdentifier(course) {
		return fmt.Errorf("%w: course identifier", ErrInvalidJobLabel)
	}
	if len(labels) > maxJobLabelBatch {
		return fmt.Errorf("%w: homework batch limit", ErrInvalidJobLabel)
	}
	normalized := make([]HomeworkLabel, len(labels))
	for i, label := range labels {
		if label.ID <= 0 {
			return fmt.Errorf("%w: homework identifier", ErrInvalidJobLabel)
		}
		name, err := normalizedJobLabelName(label.Name)
		if err != nil {
			return fmt.Errorf("%w: homework name", err)
		}
		normalized[i] = HomeworkLabel{ID: label.ID, Name: name}
	}
	if len(normalized) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	statement := `INSERT INTO job_homework_labels(course_identifier,homework_id,name,updated_at) VALUES(?,?,?,?)
ON CONFLICT(course_identifier,homework_id) DO NOTHING`
	if overwrite {
		statement = `INSERT INTO job_homework_labels(course_identifier,homework_id,name,updated_at) VALUES(?,?,?,?)
ON CONFLICT(course_identifier,homework_id) DO UPDATE SET name=excluded.name,updated_at=excluded.updated_at
WHERE job_homework_labels.name<>excluded.name`
	}
	for _, label := range normalized {
		_, err = tx.ExecContext(ctx, statement, course, label.ID, label.Name, stamp)
		if err != nil {
			return fmt.Errorf("remember homework display label: %w", err)
		}
	}
	return tx.Commit()
}

// LookupJobLabels returns empty names for unknown metadata. homeworkID==0 asks
// only for the course label, which also supports course-scoped jobs. The result
// is solely for display; absence of a label must never broaden authorization.
func (s *Store) LookupJobLabels(ctx context.Context, course string, homeworkID int64) (courseName, homeworkName string, err error) {
	if !validCourseLabelIdentifier(course) || homeworkID < 0 {
		return "", "", ErrInvalidJobLabel
	}
	err = s.db.QueryRowContext(ctx, `SELECT
COALESCE((SELECT name FROM job_course_labels WHERE course_identifier=?),''),
COALESCE((SELECT name FROM job_homework_labels WHERE course_identifier=? AND homework_id=?),'')`, course, course, homeworkID).Scan(&courseName, &homeworkName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return courseName, homeworkName, err
}
