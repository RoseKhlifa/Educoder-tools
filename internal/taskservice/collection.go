package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type CollectionLimits struct {
	Pages, Courses, Homeworks, Levels int
	Timeout                           time.Duration
}

func DefaultCollectionLimits() CollectionLimits {
	return CollectionLimits{5, 20, 100, 500, 3 * time.Minute}
}

// CollectionReader has no save/build/submit/model operation by construction.
// Production uses Client.CollectionReader's GET allowlist and byte budgets.
type CollectionReader interface {
	CollectionCoursesPage(context.Context, string, int) ([]educoder.Course, error)
	CollectionHomeworksPage(context.Context, string, int) ([]educoder.Homework, error)
	HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error)
	CollectionLoadTask(context.Context, string, int64, string) (*educoder.Task, error)
	ReadProgramming(context.Context, *educoder.Task) (*educoder.ProgrammingTask, error)
}

type CollectionSummary struct {
	Courses                int            `json:"courses"`
	Homeworks              int            `json:"homeworks"`
	Levels                 int            `json:"levels"`
	Collected              int            `json:"collected"`
	Duplicates             int            `json:"duplicates"`
	Skipped                int            `json:"skipped"`
	Unconfirmed            int            `json:"unconfirmed"`
	Failed                 int            `json:"failed"`
	LimitReached           bool           `json:"limit_reached"`
	Reasons                map[string]int `json:"reasons"`
	FullScoreLevels        int            `json:"full_score_levels"`
	LocatedLevels          int            `json:"located_levels"`
	ObjectiveAnswersRead   int            `json:"objective_answers_read"`
	ObjectiveQuestionsRead int            `json:"objective_questions_read"`
	ProgrammingFilesRead   int            `json:"programming_files_read"`
}

func (s *CollectionSummary) skip(reason string, unconfirmed bool) {
	s.Skipped++
	s.Reasons[reason]++
	if unconfirmed {
		s.Unconfirmed++
	}
}

type Collector struct {
	Bank   *questionbank.Store
	Limits CollectionLimits
}

// Scan is sequential. The web runtime additionally reserves one global scan
// slot, while the jobs manager serializes each owner's work and bounds queues.
func (h *Collector) Scan(ctx context.Context, c CollectionReader, owner string, emit func(CollectionSummary)) (summary CollectionSummary, err error) {
	summary.Reasons = map[string]int{}
	l := h.Limits
	if h.Bank == nil || c == nil || l.Pages < 1 || l.Pages > 5 || l.Courses < 1 || l.Courses > 20 || l.Homeworks < 1 || l.Homeworks > 100 || l.Levels < 1 || l.Levels > 500 || l.Timeout <= 0 || l.Timeout > 3*time.Minute {
		return summary, errors.New("invalid collection limits")
	}
	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if errors.Is(err, educoder.ErrCollectionLimit) || errors.Is(err, context.DeadlineExceeded) {
			summary.LimitReached = true
			summary.Reasons["request_byte_or_time_limit"]++
		}
		if emit != nil {
			emit(summary)
		}
	}()
	seeds, e := h.Bank.CollectionTargets(ctx, owner, 500)
	if e != nil {
		return summary, e
	}
	seenCourses := map[string]bool{}
	seenHomework := map[string]bool{}
	seenGame := map[string]bool{}
	for _, status := range []string{"processing", "end"} {
		for page := 1; page <= l.Pages; page++ {
			if err = ctx.Err(); err != nil {
				return summary, err
			}
			courses, e := c.CollectionCoursesPage(ctx, status, page)
			if e != nil {
				if errors.Is(e, educoder.ErrCollectionLimit) || ctx.Err() != nil {
					return summary, e
				}
				summary.Failed++
				summary.Reasons["course_read_failed"]++
				return summary, e
			}
			added := 0
			for _, course := range courses {
				courseID := course.Identifier
				if courseID == "" {
					courseID = educoder.CourseIdentifier(course.FirstCategoryURL)
				}
				if courseID == "" {
					summary.skip("classroom_identifier_missing", false)
					continue
				}
				if seenCourses[courseID] {
					continue
				}
				seenCourses[courseID] = true
				added++
				if summary.Courses >= l.Courses {
					summary.LimitReached = true
					return summary, nil
				}
				summary.Courses++
				for hp := 1; hp <= l.Pages; hp++ {
					hws, e := c.CollectionHomeworksPage(ctx, courseID, hp)
					if e != nil {
						if errors.Is(e, educoder.ErrCollectionLimit) || ctx.Err() != nil {
							return summary, e
						}
						summary.Failed++
						summary.Reasons["homework_read_failed"]++
						break
					}
					hwAdded := 0
					for _, hw := range hws {
						key := courseID + ":" + strconv.FormatInt(hw.ID, 10)
						if seenHomework[key] {
							continue
						}
						seenHomework[key] = true
						hwAdded++
						if summary.Homeworks >= l.Homeworks {
							summary.LimitReached = true
							return summary, nil
						}
						summary.Homeworks++
						if !hw.IsShixun || hw.ShixunIdentifier == "" {
							summary.skip("unsupported_homework_kind", false)
							continue
						}
						progress, e := c.HomeworkProgress(ctx, courseID, hw.ID)
						if e != nil {
							if errors.Is(e, educoder.ErrCollectionLimit) || ctx.Err() != nil {
								return summary, e
							}
							summary.Failed++
							summary.Reasons["progress_read_failed"]++
							continue
						}
						levels := map[int64]educoder.ChallengeProgress{}
						// Unlabelled traversal requires an independently matching
						// roster size; a truncated list of passed levels is insufficient.
						allFull := hw.ChallengeCount > 0 && len(progress.Challenges) == hw.ChallengeCount
						ambiguous := false
						for _, p := range progress.Challenges {
							if p.ChallengeID < 1 || levels[p.ChallengeID].ChallengeID != 0 {
								ambiguous = true
							}
							levels[p.ChallengeID] = p
							allFull = allFull && p.HasFullScore()
						}
						if ambiguous {
							summary.skip("ambiguous_challenge_list", true)
							continue
						}
						fullCount := 0
						for _, p := range levels {
							if p.HasFullScore() {
								fullCount++
							}
						}
						if fullCount == 0 {
							summary.skip("no_confirmed_full_score", false)
							continue
						}
						summary.FullScoreLevels += fullCount
						var targets []questionbank.CollectionTarget
						located := map[string]educoder.CollectionLocator{}
						// Existing-runtime discovery is an optional read-only protocol
						// capability, separate from saved local solver seeds.
						if resolver, ok := c.(interface {
							CollectionLocate(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (educoder.CollectionLocationResult, error)
						}); ok {
							result, resolveErr := resolver.CollectionLocate(ctx, courseID, hw, progress)
							if resolveErr != nil {
								if errors.Is(resolveErr, educoder.ErrCollectionLimit) || ctx.Err() != nil {
									return summary, resolveErr
								}
								summary.Reasons["readonly_locator_read_failed"]++
							} else {
								for reason, n := range result.Reasons {
									summary.Reasons[reason] += n
								}
								for _, target := range result.Targets {
									located[target.Game] = target
									targets = append(targets, questionbank.CollectionTarget{Course: courseID, HomeworkID: hw.ID, Game: target.Game, ChallengeID: target.ChallengeID, Shixun: hw.ShixunIdentifier})
								}
							}
						}
						for _, s := range seeds {
							if s.Course == courseID && s.HomeworkID == hw.ID && (s.Shixun == "" || s.Shixun == hw.ShixunIdentifier) {
								targets = append(targets, s)
							}
						}
						// Only fully passed homeworks may follow unlabelled links or
						// next_game: a partial homework could point at an unfinished level.
						if allFull {
							for _, game := range collectionOperationGames(hw.Operation, courseID, hw.ID) {
								targets = append(targets, questionbank.CollectionTarget{Course: courseID, HomeworkID: hw.ID, Game: game})
							}
						}
						if len(targets) == 0 {
							summary.skip("no_readonly_game_locator", true)
							continue
						}
						for _, seed := range targets {
							for game := seed.Game; game != ""; {
								if err = ctx.Err(); err != nil {
									return summary, err
								}
								if !allFull && !levels[seed.ChallengeID].HasFullScore() {
									summary.skip("target_not_confirmed_full_score", true)
									break
								}
								gkey := key + ":" + game
								if seenGame[gkey] {
									break
								}
								seenGame[gkey] = true
								if summary.Levels >= l.Levels {
									summary.LimitReached = true
									return summary, nil
								}
								summary.Levels++
								task, e := c.CollectionLoadTask(ctx, courseID, hw.ID, game)
								if e != nil {
									if errors.Is(e, educoder.ErrCollectionLimit) || ctx.Err() != nil {
										return summary, e
									}
									summary.Failed++
									summary.Reasons["task_read_failed"]++
									break
								}
								if !collectionIdentity(task, courseID, hw, game, seed.ChallengeID) {
									summary.skip("target_identity_mismatch", true)
									break
								}
								if expected, ok := located[game]; ok && (task.MyShixun.Identifier != expected.MyShixunIdentifier || task.Challenge.ID != expected.ChallengeID || task.Challenge.Position != expected.Position || task.Challenge.Subject != expected.Name) {
									summary.skip("runtime_game_challenge_identity_mismatch", true)
									break
								}
								p, ok := levels[task.Challenge.ID]
								if !ok || !p.HasFullScore() {
									summary.skip("target_not_confirmed_full_score", true)
									break
								}
								summary.LocatedLevels++
								// A fresh score read brackets answer capture. No editor,
								// submission or runtime creation endpoint is called.
								e = h.collectLevel(ctx, c, owner, task, p, &summary)
								if e != nil {
									if errors.Is(e, educoder.ErrCollectionLimit) {
										return summary, e
									}
									if ctx.Err() != nil {
										return summary, ctx.Err()
									}
									summary.Failed++
									summary.Reasons["answer_capture_failed"]++
								}
								if emit != nil {
									emit(summary)
								}
								if !allFull || task.NextGame == nil {
									break
								}
								game = *task.NextGame
								seed.ChallengeID = 0
							}
						}
					}
					if len(hws) == 0 || hwAdded == 0 {
						break
					}
					if hp == l.Pages {
						summary.LimitReached = true
						summary.Reasons["homework_page_limit"]++
					}
				}
			}
			if len(courses) == 0 || added == 0 {
				break
			}
			if page == l.Pages {
				summary.LimitReached = true
				summary.Reasons["course_page_limit"]++
			}
		}
	}
	return summary, nil
}

func collectionIdentity(t *educoder.Task, course string, hw educoder.Homework, game string, challenge int64) bool {
	return t != nil && t.Game.Identifier == game && t.Game.ID > 0 && t.Game.MyShixunID > 0 && t.MyShixun.ID == t.Game.MyShixunID && t.HomeworkID == hw.ID && t.CourseIdentifier == course && t.Shixun.Identifier == hw.ShixunIdentifier && t.Challenge.ID > 0 && (challenge == 0 || challenge == t.Challenge.ID)
}

// URLs are identifiers only: never follow a supplied host, query or operation.
// A task URL must carry the exact classroom and homework context.
var operationGame = regexp.MustCompile(`^/tasks/([A-Za-z0-9_-]{1,100})/([0-9]+)/([A-Za-z0-9_-]{1,100})/?$`)

func collectionOperationGames(raw []json.RawMessage, course string, hw int64) []string {
	var out []string
	var walk func(any, int)
	walk = func(x any, depth int) {
		if depth > 8 {
			return
		}
		switch v := x.(type) {
		case string:
			u, e := url.Parse(v)
			if e != nil || u.User != nil {
				return
			}
			m := operationGame.FindStringSubmatch(u.Path)
			if len(m) == 4 && m[1] == course && m[2] == strconv.FormatInt(hw, 10) {
				out = append(out, m[3])
			}
		case []any:
			for _, z := range v {
				walk(z, depth+1)
			}
		case map[string]any:
			for _, z := range v {
				walk(z, depth+1)
			}
		}
	}
	for _, b := range raw {
		var x any
		if json.Unmarshal(b, &x) == nil {
			walk(x, 0)
		}
	}
	return out
}

func (h *Collector) collectLevel(ctx context.Context, c CollectionReader, owner string, t *educoder.Task, p educoder.ChallengeProgress, s *CollectionSummary) error {
	if t == nil || p.ChallengeID != t.Challenge.ID || !p.HasFullScore() {
		s.skip("target_not_confirmed_full_score", true)
		return nil
	}
	switch t.Kind() {
	case educoder.TaskKindProgramming:
		if t.Game.Status != 2 || len(t.TestSets) == 0 {
			s.skip("programming_evaluation_unconfirmed", true)
			return nil
		}
		for _, test := range t.TestSets {
			if test.Result == nil || !*test.Result || test.CompileSuccess == nil || *test.CompileSuccess != 1 {
				s.skip("programming_evaluation_unconfirmed", true)
				return nil
			}
		}
		// The observed GET API contains no submitted-source digest/revision.
		// Only an exact local, owner/target-bound formal record can bridge this.
		pt, err := c.ReadProgramming(ctx, t)
		if err != nil {
			return err
		}
		s.ProgrammingFilesRead += len(pt.Files)
		snap, files := collectionCodeSnapshot(t, pt)
		prepared, err := problem.Prepare(snap)
		if err != nil {
			return err
		}
		hash, err := problem.HashFiles(files)
		if err != nil {
			return err
		}
		confirmed, err := h.Bank.HasCollectionCodeEvidence(ctx, owner, questionbank.CollectionTarget{Course: t.CourseIdentifier, HomeworkID: t.HomeworkID, Game: t.Game.Identifier, ChallengeID: t.Challenge.ID}, questionbank.Ref{RevisionHash: prepared.RevisionHash, ContractHash: prepared.ContractHash}, hash)
		if err != nil {
			return err
		}
		if !confirmed {
			s.skip("programming_submitted_source_unproven", true)
			return nil
		}
		if err = collectionFreshScore(ctx, c, t, p); err != nil {
			s.skip("score_changed_or_unconfirmed", true)
			return nil
		}
		again, err := c.ReadProgramming(ctx, t)
		if err != nil {
			return err
		}
		_, finalFiles := collectionCodeSnapshot(t, again)
		finalHash, err := problem.HashFiles(finalFiles)
		if err != nil || hash != finalHash || !reflect.DeepEqual(pt.Files, again.Files) {
			s.skip("editor_changed", true)
			return nil
		}
		// This exact verified solution already exists. Do not manufacture a new
		// platform verification or append duplicate evidence on every login.
		s.Duplicates++
		return nil
	case educoder.TaskKindChoice:
		s.ObjectiveQuestionsRead += len(t.Chooses)
		for _, test := range t.ChooseTestCases.TestSets {
			if raw, ok := test.ActualOutput.(string); ok && strings.TrimSpace(raw) != "" {
				s.ObjectiveAnswersRead++
			}
		}
		snapshot, answers, reason := collectionObjective(t)
		if reason != "" {
			s.skip(reason, true)
			return nil
		}
		if err := collectionFreshScore(ctx, c, t, p); err != nil {
			s.skip("score_changed_or_unconfirmed", true)
			return nil
		}
		fresh, err := c.CollectionLoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
		if err != nil {
			return err
		}
		fs, fa, fr := collectionObjective(fresh)
		if fr != "" || !collectionIdentity(fresh, t.CourseIdentifier, educoder.Homework{ID: t.HomeworkID, ShixunIdentifier: t.Shixun.Identifier}, t.Game.Identifier, t.Challenge.ID) || !reflect.DeepEqual(fs, snapshot) || !reflect.DeepEqual(fa, answers) {
			s.skip("objective_answer_changed_or_mismatched", true)
			return nil
		}
		scopeBytes, _ := json.Marshal(struct {
			Game      string
			Challenge int64
			Snapshot  answer.ObjectiveSnapshot
			Answers   []string
		}{t.Game.Identifier, t.Challenge.ID, snapshot, answers})
		scope := problem.HashContent(string(scopeBytes))
		for i, q := range snapshot.Questions {
			key, err := h.Bank.UpsertObjectiveQuestion(ctx, q)
			if err != nil {
				return err
			}
			existing, err := h.Bank.LookupObjectiveAnswers(ctx, key)
			if err != nil {
				return err
			}
			found := false
			for _, a := range existing {
				if a.Reusable && a.Answer == answers[i] {
					found = true
					break
				}
			}
			if found {
				s.Duplicates++
				continue
			}
			id, err := h.Bank.AddObjectiveAnswer(ctx, key, answers[i], "platform_collection", "")
			if err != nil {
				return err
			}
			if err = h.Bank.RecordObjectiveValidation(ctx, id, questionbank.ObjectiveValidation{Passed: true, Confirmed: true, FullScore: true, Score: p.GameScore, MaxScore: p.ChallengeScore, AnswerHash: problem.HashContent(answers[i]), ScopeHash: scope, Feedback: "Read-only collection: exact target full score and complete persisted answer sheet; matching question identities and answers confirmed by two task reads; no explicit conflicting correctness result."}); err != nil {
				return err
			}
			s.Collected++
		}
		return nil
	default:
		s.skip("unsupported_task_kind", false)
		return nil
	}
}

func collectionFreshScore(ctx context.Context, c CollectionReader, t *educoder.Task, before educoder.ChallengeProgress) error {
	p, err := c.HomeworkProgress(ctx, t.CourseIdentifier, t.HomeworkID)
	if err != nil {
		return err
	}
	count := 0
	for _, v := range p.Challenges {
		if v.ChallengeID == t.Challenge.ID {
			count++
			if !v.HasFullScore() || v.GameScore != before.GameScore || v.ChallengeScore != before.ChallengeScore {
				return errors.New("score changed")
			}
		}
	}
	if count != 1 {
		return errors.New("target score missing or ambiguous")
	}
	return nil
}

func collectionCodeSnapshot(t *educoder.Task, pt *educoder.ProgrammingTask) (problem.Snapshot, []problem.SolutionFile) {
	s := problem.Snapshot{SchemaVersion: problem.SchemaVersion, Kind: "programming", Title: t.Challenge.Subject, StatementMarkdown: t.Challenge.Description, EditMode: "single_file", SnapshotCompleteness: "observed", PublicTests: []problem.PublicTest{}, EnvironmentContract: []string{"editor:" + strings.ToLower(strings.TrimSpace(t.CodeEditor.Name))}}
	var files []problem.SolutionFile
	seen := map[string]bool{}
	for _, f := range pt.Files {
		s.Files = append(s.Files, problem.File{Path: f.Path, Filename: f.Filename, Extension: f.Extension, Language: f.Language, CurrentContent: f.Content, StarterProvenance: "unknown"})
		files = append(files, problem.SolutionFile{Path: f.Path, Content: f.Content})
		if !seen[f.Language] {
			s.EnvironmentContract = append(s.EnvironmentContract, "language:"+f.Language)
			seen[f.Language] = true
		}
	}
	for _, test := range t.TestSets {
		if test.IsPublic {
			s.PublicTests = append(s.PublicTests, problem.PublicTest{Stdin: test.Input, ExpectedStdout: test.Output})
		}
	}
	return s, files
}

var collectionMedia = regexp.MustCompile(`(?i)<(?:img|svg|object|embed|video|audio|canvas|iframe)\b|!\[`)

func collectionObjective(t *educoder.Task) (answer.ObjectiveSnapshot, []string, string) {
	s := answer.ObjectiveSnapshot{Kind: "objective"}
	var answers []string
	if t == nil || t.Kind() != educoder.TaskKindChoice || t.Game.Status != 2 {
		return s, nil, "objective_status_unconfirmed"
	}
	s.Title = t.Challenge.Subject
	s.Statement = t.Challenge.Description
	qs := append([]educoder.ChoiceQuestion(nil), t.Chooses...)
	sort.Slice(qs, func(i, j int) bool { return qs[i].Position < qs[j].Position })
	tests := t.ChooseTestCases
	// Educoder hides correct-count and per-question results when the teacher
	// disables answer visibility. The persisted complete submitted answer sheet
	// can still be bound to this exact challenge's independently read full score
	// in collectLevel. Submitted flags alone NEVER establish correctness.
	hiddenResults := tests.Correct == nil
	if len(qs) == 0 || tests.Count != len(qs) || (tests.Correct != nil && *tests.Correct != len(qs)) || (hiddenResults && (!tests.HadSubmit || !tests.HadAllSubmit)) || len(tests.TestSets) != len(qs) {
		return s, nil, "objective_answer_evidence_missing"
	}
	positions := map[int]bool{}
	ids := map[int64]bool{}
	for _, q := range qs {
		if q.QuestionType != 0 || (q.Category != 1 && q.Category != 2) {
			return s, nil, "unsupported_objective_question_kind"
		}
		if q.ChallengeID != t.Challenge.ID || q.ChallengeChooseID < 1 || ids[q.ChallengeChooseID] || q.Position < 1 || positions[q.Position] {
			return s, nil, "objective_question_identity_mismatch"
		}
		positions[q.Position] = true
		ids[q.ChallengeChooseID] = true
		if collectionMedia.MatchString(q.Subject) {
			return s, nil, "objective_media_content_unproven"
		}
		x := answer.ObjectiveQuestion{Position: q.Position, Question: q.Subject, QuestionType: q.QuestionType, Category: q.Category}
		options := append([]educoder.ChoiceOption(nil), q.Options...)
		sort.Slice(options, func(i, j int) bool { return options[i].Position < options[j].Position })
		for _, o := range options {
			if collectionMedia.MatchString(o.OptionName) {
				return s, nil, "objective_media_content_unproven"
			}
			x.Options = append(x.Options, answer.ObjectiveOption{Position: o.Position, Label: string(rune('A' + o.Position)), Text: o.OptionName})
		}
		found := 0
		answerText := ""
		for _, test := range tests.TestSets {
			if test.Position != q.Position {
				continue
			}
			found++
			if test.QuestionName != q.QuestionName || test.QuestionType != q.QuestionType || (test.Result != nil && !*test.Result) || (test.Result == nil && (!tests.HadSubmit || !tests.HadAllSubmit)) {
				return s, nil, "objective_result_mismatch"
			}
			raw, ok := test.ActualOutput.(string)
			if !ok {
				return s, nil, "objective_actual_answer_missing"
			}
			var err error
			answerText, err = educoder.CanonicalizeChoiceAnswer(q, raw)
			if err != nil {
				return s, nil, "objective_actual_answer_invalid"
			}
		}
		if found != 1 {
			return s, nil, "objective_result_position_mismatch"
		}
		s.Questions = append(s.Questions, x)
		answers = append(answers, answerText)
	}
	if collectionMedia.MatchString(s.Statement) {
		return s, nil, "objective_media_content_unproven"
	}
	if err := answer.ValidateObjectiveCandidate(s, answer.ObjectiveCandidate{Kind: "objective", Answers: answers}); err != nil {
		return s, nil, "objective_contract_invalid"
	}
	return s, answers, ""
}

func CollectionMessage(s CollectionSummary) string {
	return fmt.Sprintf("已采集 %d，重复 %d，跳过 %d，未确认 %d，失败 %d", s.Collected, s.Duplicates, s.Skipped, s.Unconfirmed, s.Failed)
}
