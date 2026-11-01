package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"sync"

	"educoder-cli/internal/educoder"
)

// Exercise reads remain optional on test/client implementations. Production
// uses the same Go client and authenticated cookie jar as the classroom list.
type exerciseListClient interface {
	Exercises(context.Context, string) ([]educoder.Exercise, error)
	ExercisePaper(context.Context, string, int64) (*educoder.ExercisePaper, error)
	ExerciseUserInfo(context.Context, string, int64) (*educoder.ExerciseUserInfo, error)
	ExerciseSubmission(context.Context, string, int64) (*educoder.ExerciseSubmission, error)
	ExerciseTime(context.Context, string, int64) (*educoder.ExerciseTime, error)
	ExerciseOverview(context.Context, string, int64) (*educoder.ExerciseOverview, error)
	ExerciseWriteGuard(context.Context, string, int64) (*educoder.ExercisePaper, error)
}

type exerciseDTO struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Submitted      bool   `json:"submitted"`
	ScoreKnown     bool   `json:"scoreKnown"`
	Score          string `json:"score,omitempty"`
	MaxScore       string `json:"maxScore,omitempty"`
	TotalQuestions int    `json:"totalQuestions,omitempty"`
	Deadline       string `json:"deadline,omitempty"`
	Available      bool   `json:"available"`
	ActionRequired bool   `json:"actionRequired"`
	ReasonCode     string `json:"reasonCode,omitempty"`
	URL            string `json:"url"`
}

func (s *Server) handleExercises(w http.ResponseWriter, r *http.Request, requestID, course string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	if educoderIdentifier(course) != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_COURSE", "课堂标识无效")
		return
	}
	client, ok := session.Client.(exerciseListClient)
	if !ok {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "EXERCISES_UNAVAILABLE", "考试列表暂未同步，请稍后查看")
		return
	}
	// Check membership before reading a supplied classroom's exam list.
	courses, err := session.Client.Courses(r.Context(), "processing")
	if err != nil {
		s.upstreamError(w, requestID)
		return
	}
	member := false
	for _, item := range courses {
		member = member || item.Identifier == course
	}
	if !member {
		s.writeError(w, requestID, http.StatusNotFound, "COURSE_NOT_FOUND", "课堂不存在或不属于当前账号")
		return
	}
	s.rememberCourseLabels(r.Context(), courses)
	exams, err := client.Exercises(r.Context(), course)
	if err != nil || len(exams) > 100 {
		s.upstreamError(w, requestID)
		return
	}
	items := make([]exerciseDTO, len(exams))
	// Read-only metadata fanout is bounded and never creates/starts an attempt.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, exam := range exams {
		wg.Add(1)
		go func(i int, exam educoder.Exercise) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-r.Context().Done():
				return
			}
			items[i] = readExerciseDTO(r.Context(), client, course, exam)
		}(i, exam)
	}
	wg.Wait()
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"items": items, "courseIdentifier": course})
}

func readExerciseDTO(ctx context.Context, client exerciseListClient, course string, exam educoder.Exercise) exerciseDTO {
	out := exerciseDTO{ID: exam.ID, Name: adminContextText(exam.Name, 500), Status: "action_required", ActionRequired: true, ReasonCode: "upstream_error", URL: fmt.Sprintf("https://www.educoder.net/classrooms/%s/exercise", course)}
	// Teachers may disable paper access after submission. The submitted state
	// must remain visible through the independently bound metadata endpoints.
	if overview, err := client.ExerciseOverview(ctx, course, exam.ID); err == nil && overview != nil {
		out.Name = adminContextText(overview.Name, 500)
		out.TotalQuestions, out.Deadline = overview.TotalQuestions, overview.EndTime
		var score any
		if decodeExerciseNumber(overview.MaxScore, &score) == nil {
			out.MaxScore = fmt.Sprint(score)
		}
	}
	submission, err := client.ExerciseSubmission(ctx, course, exam.ID)
	if err != nil || submission == nil {
		return out
	}
	if submission.ConfirmedSubmitted {
		out.Submitted, out.ActionRequired, out.ReasonCode, out.Status = true, false, "", "submitted"
		if submission.OpenTotalScore {
			var score any
			if jsonErr := decodeExerciseNumber(submission.Score, &score); jsonErr == nil {
				out.ScoreKnown, out.Score, out.Status = true, fmt.Sprint(score), "graded"
			}
		}
		return out
	}
	paper, err := client.ExercisePaper(ctx, course, exam.ID)
	if err != nil || paper == nil {
		out.ReasonCode = string(educoder.ExerciseErrorCode(err))
		return out
	}
	if paper.Exercise.Name != "" {
		out.Name = adminContextText(paper.Exercise.Name, 500)
	}
	out.TotalQuestions = len(paper.Questions)
	out.Deadline = paper.Exercise.EndTime
	info, err := client.ExerciseUserInfo(ctx, course, exam.ID)
	if err != nil || info == nil {
		return out
	}
	clock, err := client.ExerciseTime(ctx, course, exam.ID)
	if err != nil || clock == nil {
		return out
	}
	if clock.UserEndTime != "" {
		out.Deadline = clock.UserEndTime
	}
	if clock.LeftTime <= 0 && clock.StateKnown {
		out.Status, out.ReasonCode = "expired", "expired"
		return out
	}
	if info.IsUserLocked || info.StartLocked {
		out.ReasonCode = "locked"
		return out
	}
	if !info.UserIsEnter || !info.CanStart || paper.Exercise.ExerciseStartAt == "" {
		out.Status, out.ReasonCode = "not_started", "not_started"
		return out
	}
	if _, err := client.ExerciseWriteGuard(ctx, course, exam.ID); err != nil {
		out.ReasonCode = string(educoder.ExerciseErrorCode(err))
		if out.ReasonCode == "expired" {
			out.Status = "expired"
		}
		return out
	}
	out.Available, out.ActionRequired, out.ReasonCode, out.Status = true, false, "", "available"
	return out
}

var exerciseDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func decodeExerciseNumber(raw []byte, out *any) error {
	// Scores may be decimal JSON numbers or strings. Refuse hidden/non-numeric
	// values rather than presenting a missing score as zero.
	text := string(raw)
	if len(text) > 1 && text[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return fmt.Errorf("score not public")
		}
	}
	if len(text) > 80 || !exerciseDecimal.MatchString(text) {
		return fmt.Errorf("score not public")
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || value.Sign() < 0 {
		return fmt.Errorf("score not public")
	}
	*out = text
	return nil
}
