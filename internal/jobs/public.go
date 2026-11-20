package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var label = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
var credentialText = regexp.MustCompile(`(?i)(bearer\s+[A-Za-z0-9._~-]+|(?:authorization|cookie|password|passwd|api[_-]?key|access[_-]?token|refresh[_-]?token|sec_key|secret|x-ed[c]?oder-signature)\s*[:=]|(?:[?&])(?:token|signature|sign|access_key|sec_key|secret|x-oss-signature|x-amz-signature)=)`)

func normalize(req Request) (Request, string, string, error) {
	req.Course = strings.TrimSpace(req.Course)
	req.Game = strings.TrimSpace(req.Game)
	collect := req.Mode == "collect_verified"
	if collect && (req.Course != "" || req.HomeworkID != 0 || req.ExerciseID != 0 || req.Game != "" || req.Scope != "") {
		return req, "", "", fail("invalid_request", "Collection scans the authenticated account, not a supplied target")
	}
	// Older workbench bundles submitted the current game for a normal submit
	// action and omitted scope. A submit is now a whole-homework operation, so
	// normalize that legacy shape before clearing the explicit current alias.
	// This keeps stale browser bundles from silently running one level at a time.
	if req.Scope == "" && req.Mode == "submit" && req.Game != "" {
		req.Scope = "homework"
		req.Game = ""
	}
	// Canonicalize explicit current to empty so published idempotency payloads
	// remain valid for the generate-only and self-test current-level modes.
	if req.Scope == "current" {
		req.Scope = ""
	}
	if req.Scope != "" && req.Scope != "homework" && req.Scope != "course" && req.Scope != "all" && req.Scope != "exercise" {
		return req, "", "", fail("invalid_request", "Unknown execution scope")
	}
	if req.Scope != "exercise" && req.ExerciseID != 0 {
		return req, "", "", fail("invalid_request", "Exercise target requires exercise scope")
	}
	if req.Scope == "homework" && (req.Mode != "submit" || req.Game != "") {
		return req, "", "", fail("invalid_request", "Whole-homework jobs require submit mode and no current game")
	}
	switch req.Scope {
	case "exercise":
		if req.Mode != "submit" || !identifier.MatchString(req.Course) || req.ExerciseID <= 0 || req.HomeworkID != 0 || req.Game != "" {
			return req, "", "", fail("invalid_request", "Exercise jobs require a course, exercise_id, submit mode and no homework or game")
		}
	case "course":
		if req.Mode != "submit" || !identifier.MatchString(req.Course) || req.HomeworkID != 0 || req.Game != "" {
			return req, "", "", fail("invalid_request", "Whole-course jobs require a course, submit mode and no homework or game")
		}
	case "all":
		if req.Mode != "submit" || req.Course != "" || req.HomeworkID != 0 || req.Game != "" {
			return req, "", "", fail("invalid_request", "Account-wide jobs require submit mode and no supplied target")
		}
	default:
		if !collect && (!identifier.MatchString(req.Course) || req.HomeworkID < 1 || (req.Game != "" && !identifier.MatchString(req.Game))) {
			return req, "", "", fail("invalid_request", "A valid course, homework_id and optional game are required")
		}
	}
	if !collect && req.Mode != "generate_only" && req.Mode != "selftest" && req.Mode != "submit" {
		return req, "", "", fail("invalid_request", "Unknown execution mode")
	}
	if len(req.IdempotencyKey) > 128 || strings.IndexFunc(req.IdempotencyKey, unicode.IsControl) >= 0 || credentialText.MatchString(req.IdempotencyKey) {
		return req, "", "", fail("invalid_request", "Invalid idempotency key")
	}
	p := req
	p.IdempotencyKey = ""
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	t := struct {
		Course     string
		HomeworkID int64
		Game, Mode string
		Scope      string `json:",omitempty"`
		ExerciseID int64  `json:",omitempty"`
	}{req.Course, req.HomeworkID, req.Game, req.Mode, req.Scope, req.ExerciseID}
	tb, _ := json.Marshal(t)
	th := sha256.Sum256(tb)
	return req, hex.EncodeToString(h[:]), hex.EncodeToString(th[:]), nil
}

func publicText(s, fallback string) string {
	if credentialText.MatchString(s) {
		return fallback
	}
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s))
	if len(s) > 1024 {
		s = s[:1024]
		for !bytes.Equal([]byte(s), []byte(strings.ToValidUTF8(s, ""))) {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		return fallback
	}
	return s
}

func publicLabel(s, fallback string) string {
	if !label.MatchString(s) {
		return fallback
	}
	return s
}

func publicResult(raw json.RawMessage, limit int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > limit {
		return nil, fail("result_too_large", "Job result exceeded the storage limit")
	}
	if !json.Valid(raw) {
		return nil, fail("invalid_result", "Executor returned invalid JSON")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, fail("invalid_result", "Executor returned invalid JSON")
	}
	if !validatePublic(value, 0) {
		return nil, fail("private_result", "Executor result contained private fields")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func validatePublic(v any, depth int) bool {
	if depth > 24 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			switch strings.ToLower(strings.ReplaceAll(k, "-", "_")) {
			case "password", "passwd", "cookie", "cookies", "authorization", "api_key", "apikey", "access_token", "refresh_token", "session_cookie", "sec_key", "secret", "token":
				return false
			}
			if !validatePublic(v, depth+1) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !validatePublic(v, depth+1) {
				return false
			}
		}
	case string:
		if credentialText.MatchString(x) {
			return false
		}
		if u, e := url.Parse(x); e == nil && u.IsAbs() && u.User != nil {
			return false
		}
	}
	return true
}

func mutatingPhase(phase string) bool {
	p := strings.ToLower(phase)
	return strings.Contains(p, "submitting") || strings.Contains(p, "evaluating") || p == "saving" || p == "submit" || p == "evaluation" || p == "formal_eval" || p == "formal_submit"
}

func clone(j Job) Job {
	j.Result = append(json.RawMessage(nil), j.Result...)
	j.Events = append([]Event(nil), j.Events...)
	for i := range j.Events {
		j.Events[i].Progress = cloneProgress(j.Events[i].Progress)
	}
	j.Progress = cloneProgress(j.Progress)
	if j.StartedAt != nil {
		t := *j.StartedAt
		j.StartedAt = &t
	}
	if j.FinishedAt != nil {
		t := *j.FinishedAt
		j.FinishedAt = &t
	}
	return j
}

func cloneProgress(p *BatchProgress) *BatchProgress {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Homeworks = cloneHomeworks(p.Homeworks)
	return &copy
}

func cloneHomeworks(items []HomeworkProgress) []HomeworkProgress {
	if len(items) == 0 {
		return nil
	}
	out := make([]HomeworkProgress, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Levels = append([]LevelProgress(nil), item.Levels...)
		if item.StartedAt != nil {
			at := *item.StartedAt
			out[i].StartedAt = &at
		}
		if item.UpdatedAt != nil {
			at := *item.UpdatedAt
			out[i].UpdatedAt = &at
		}
	}
	return out
}

// eventProgress keeps the per-event history to counters only. A job keeps up to
// EventLimit events, so repeating the per-assignment detail in each of them
// would multiply the stored job size by the event limit.
func eventProgress(p *BatchProgress) *BatchProgress {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Homeworks = nil
	return &copy
}

func publicProgress(p *BatchProgress) *BatchProgress {
	if p == nil || p.TotalLevels < 0 || p.TotalLevels > 100 || p.CompletedLevels < 0 || p.CompletedLevels > p.TotalLevels || p.SkippedLevels < 0 || p.SkippedLevels > p.CompletedLevels || p.FailedLevels < 0 || p.FailedLevels > p.TotalLevels || p.CurrentPosition < 0 || p.CurrentPosition > 500 {
		return nil
	}
	if p.TotalQuestions < 0 || p.TotalQuestions > 256 || p.CompletedQuestions < 0 || p.CompletedQuestions > p.TotalQuestions || p.CurrentExerciseID < 0 {
		return nil
	}
	if p.TotalHomeworks < 0 || p.TotalHomeworks > 100 || p.CompletedHomeworks < 0 || p.CompletedHomeworks > p.TotalHomeworks || p.SkippedHomeworks < 0 || p.SkippedHomeworks > p.CompletedHomeworks || p.NeedsServiceHomeworks < 0 || p.CompletedHomeworks+p.NeedsServiceHomeworks > p.TotalHomeworks || (p.TotalLevels == 0 && p.TotalHomeworks == 0 && p.TotalQuestions == 0 && len(p.Homeworks) == 0) || p.CurrentHomeworkID < 0 {
		return nil
	}
	result := *p
	result.CurrentTitle = publicText(result.CurrentTitle, "")
	result.CurrentHomeworkTitle = publicText(result.CurrentHomeworkTitle, "")
	result.CurrentExerciseTitle = publicText(result.CurrentExerciseTitle, "")
	result.Stage = publicLabel(result.Stage, "")
	if result.CurrentCourse != "" && !identifier.MatchString(result.CurrentCourse) {
		result.CurrentCourse = ""
	}
	result.Homeworks = publicHomeworks(p.Homeworks)
	return &result
}

// publicHomeworks bounds and sanitizes the per-assignment detail. An entry that
// cannot be represented publicly is dropped rather than published partially,
// and the whole list is capped so no upstream plan can grow a stored job.
func publicHomeworks(items []HomeworkProgress) []HomeworkProgress {
	if len(items) == 0 {
		return nil
	}
	out := make([]HomeworkProgress, 0, min(len(items), 100))
	for _, item := range items {
		if len(out) >= 100 {
			break
		}
		if item.HomeworkID < 0 || item.TotalLevels < 0 || item.TotalLevels > 100 || item.CurrentPosition < 0 || item.CurrentPosition > 500 {
			continue
		}
		if !progressStatus(item.Status) {
			continue
		}
		entry := item
		entry.Title = publicText(item.Title, "")
		entry.CourseName = publicText(item.CourseName, "")
		entry.CurrentTitle = publicText(item.CurrentTitle, "")
		entry.Stage = publicLabel(item.Stage, "")
		entry.ReasonCode = publicLabel(item.ReasonCode, "")
		if entry.Course != "" && !identifier.MatchString(entry.Course) {
			entry.Course = ""
		}
		entry.CompletedLevels = clampCount(item.CompletedLevels, entry.TotalLevels)
		entry.SkippedLevels = clampCount(item.SkippedLevels, entry.CompletedLevels)
		entry.FailedLevels = clampCount(item.FailedLevels, entry.TotalLevels)
		entry.Levels = publicLevels(item.Levels)
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func publicLevels(items []LevelProgress) []LevelProgress {
	if len(items) == 0 {
		return nil
	}
	out := make([]LevelProgress, 0, min(len(items), 100))
	seen := map[int]bool{}
	for _, item := range items {
		if len(out) >= 100 {
			break
		}
		if item.Position < 0 || item.Position > 500 || seen[item.Position] || !progressStatus(item.Status) {
			continue
		}
		seen[item.Position] = true
		out = append(out, LevelProgress{Position: item.Position, Title: publicText(item.Title, ""), Status: item.Status})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// progressStatus is the closed enum the live views render. An unknown status is
// rejected so a new execution state cannot leak through as display text.
func progressStatus(status string) bool {
	switch status {
	case "waiting", "running", "passed", "skipped", "failed", "stopped", "needs_attention":
		return true
	}
	return false
}

func clampCount(value, limit int) int {
	if value < 0 {
		return 0
	}
	if value > limit {
		return limit
	}
	return value
}
