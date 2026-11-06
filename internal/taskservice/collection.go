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

// Zero values mean exhaustive traversal without an aggregate scan deadline.
// Positive values remain available for explicitly bounded callers.
type CollectionLimits struct {
	Pages, Courses, Homeworks, Levels int
	Timeout                           time.Duration
}

func DefaultCollectionLimits() CollectionLimits {
	return CollectionLimits{}
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
	ScanExhausted          bool           `json:"scan_exhausted"`
	Reasons                map[string]int `json:"reasons"`
	FullScoreLevels        int            `json:"full_score_levels"`
	LocatedLevels          int            `json:"located_levels"`
	ObjectiveAnswersRead   int            `json:"objective_answers_read"`
	ObjectiveQuestionsRead int            `json:"objective_questions_read"`
	ProgrammingFilesRead   int            `json:"programming_files_read"`
	ReusedLevels           int            `json:"reused_levels"`
	ReusedQuestions        int            `json:"reused_questions"`
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
	Now    func() time.Time
}

// Scan is sequential. The web runtime additionally reserves one global scan
// slot, while the jobs manager serializes each owner's work and bounds queues.
func (h *Collector) Scan(ctx context.Context, c CollectionReader, owner string, emit func(CollectionSummary)) (summary CollectionSummary, err error) {
	summary.Reasons = map[string]int{}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	l := h.Limits
	if h.Bank == nil || c == nil || l.Pages < 0 || l.Courses < 0 || l.Homeworks < 0 || l.Levels < 0 || l.Timeout < 0 {
		return summary, errors.New("invalid collection limits")
	}
	if l.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, l.Timeout)
		defer cancel()
	}
	defer func() {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if errors.Is(err, educoder.ErrCollectionLimit) || (l.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			summary.LimitReached = true
			summary.Reasons["request_byte_or_time_limit"]++
		}
		if errors.Is(err, context.DeadlineExceeded) && !summary.LimitReached && summary.Failed == 0 {
			summary.Failed++
			summary.Reasons["request_timeout"]++
		}
		if err != nil {
			summary.ScanExhausted = false
		}
		if emit != nil {
			emit(summary)
		}
	}()
	seenCourses := map[string]bool{}
	seenHomework := map[string]bool{}
	seenGame := map[string]bool{}
	for _, status := range []string{"processing", "end"} {
		coursePages := map[string]bool{}
		for page := 1; ; page++ {
			if l.Pages > 0 && page > l.Pages {
				summary.LimitReached = true
				summary.Reasons["course_page_limit"]++
				break
			}
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
			if len(courses) == 0 {
				break
			}
			coursePageIDs := make([]string, 0, len(courses))
			for _, row := range courses {
				id := row.Identifier
				if id == "" {
					id = educoder.CourseIdentifier(row.FirstCategoryURL)
				}
				coursePageIDs = append(coursePageIDs, id)
			}
			if collectionRepeatedPage(coursePages, coursePageIDs) {
				summary.Failed++
				summary.Reasons["course_pagination_stalled"]++
				return summary, errors.New("collection course pagination repeated a nonempty page")
			}
			for _, course := range courses {
				courseID := course.Identifier
				if courseID == "" {
					courseID = educoder.CourseIdentifier(course.FirstCategoryURL)
				}
				if courseID == "" {
					summary.Failed++
					summary.skip("classroom_identifier_missing", false)
					continue
				}
				if seenCourses[courseID] {
					continue
				}
				seenCourses[courseID] = true
				if l.Courses > 0 && summary.Courses >= l.Courses {
					summary.LimitReached = true
					return summary, nil
				}
				summary.Courses++
				homeworkPages := map[string]bool{}
				for hp := 1; ; hp++ {
					if l.Pages > 0 && hp > l.Pages {
						summary.LimitReached = true
						summary.Reasons["homework_page_limit"]++
						break
					}
					hws, e := c.CollectionHomeworksPage(ctx, courseID, hp)
					if e != nil {
						if errors.Is(e, educoder.ErrCollectionLimit) || ctx.Err() != nil {
							return summary, e
						}
						summary.Failed++
						summary.Reasons["homework_read_failed"]++
						break
					}
					if len(hws) == 0 {
						break
					}
					homeworkPageIDs := make([]string, 0, len(hws))
					for _, row := range hws {
						homeworkPageIDs = append(homeworkPageIDs, strconv.FormatInt(row.ID, 10))
					}
					if collectionRepeatedPage(homeworkPages, homeworkPageIDs) {
						summary.Failed++
						summary.Reasons["homework_pagination_stalled"]++
						return summary, errors.New("collection homework pagination repeated a nonempty page")
					}
					for _, hw := range hws {
						key := courseID + ":" + strconv.FormatInt(hw.ID, 10)
						if seenHomework[key] {
							continue
						}
						seenHomework[key] = true
						if l.Homeworks > 0 && summary.Homeworks >= l.Homeworks {
							summary.LimitReached = true
							return summary, nil
						}
						summary.Homeworks++
						if !hw.IsShixun || hw.ShixunIdentifier == "" {
							summary.skip("unsupported_homework_kind", false)
							continue
						}
						progress, e := c.HomeworkProgress(ctx, courseID, hw.ID)
						if e == nil && progress == nil {
							e = errors.New("missing homework progress")
						}
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
						receipts, e := h.Bank.CollectionReceipts(ctx, owner, courseID, hw.ID)
						if e != nil {
							return summary, e
						}
						fresh := freshCollectionReceipts(hw, progress, receipts, now())
						// Only a complete current roster can suppress discovery. A
						// partial progress response is never a success watermark.
						if hw.ChallengeCount > 0 && len(progress.Challenges) == hw.ChallengeCount && len(fresh) == fullCount {
							for _, receipt := range fresh {
								if l.Levels > 0 && summary.Levels >= l.Levels {
									summary.LimitReached = true
									return summary, nil
								}
								reuseCollectionReceipt(&summary, receipt)
							}
							continue
						}
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
								summary.Failed++
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
						seeds, e := h.Bank.CollectionTargetsForHomework(ctx, owner, courseID, hw.ID)
						if e != nil {
							return summary, e
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
						mapped := map[int64]bool{}
						for _, target := range located {
							if target.Game != "" && levels[target.ChallengeID].HasFullScore() {
								mapped[target.ChallengeID] = true
							}
						}
						allTargetsKnown := hw.ChallengeCount > 0 && len(progress.Challenges) == hw.ChallengeCount && len(mapped) == fullCount
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
								if l.Levels > 0 && summary.Levels >= l.Levels {
									summary.LimitReached = true
									return summary, nil
								}
								// Skipping a linked level must not cut the only route to
								// an unknown next_game. Require independent full coverage.
								if receipt, ok := fresh[seed.ChallengeID]; ok && (!allFull || allTargetsKnown) {
									reuseCollectionReceipt(&summary, receipt)
									delete(fresh, seed.ChallengeID)
									break
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
								receipt := questionbank.CollectionReceipt{Fingerprint: collectionFingerprint(hw, progress, p), VerifiedAt: now()}
								beforeSkipped, beforeUnconfirmed := summary.Skipped, summary.Unconfirmed
								e = h.collectLevel(ctx, c, owner, task, p, &summary, &receipt)
								if e == nil && summary.Skipped == beforeSkipped && summary.Unconfirmed == beforeUnconfirmed && receipt.Questions > 0 {
									e = h.Bank.PutCollectionReceipt(ctx, owner, courseID, hw.ID, p.ChallengeID, receipt)
								}
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
					if emit != nil {
						emit(summary)
					}
				}
			}
			if emit != nil {
				emit(summary)
			}
		}
	}
	// Only arrival here proves both classroom streams and each homework
	// pager were exhausted. Budget/cancellation/error paths never set this.
	summary.ScanExhausted = !summary.LimitReached && summary.Failed == 0
	return summary, nil
}

// A page with only globally seen IDs is not exhaustion (active/ended lists and
// shifting pages may overlap). Canonical row identities ignore mutable
// labels/counts and ordering, so a broken pager cannot hide an endless repeated
// page behind changing metadata. Fail explicitly rather than call the scan done.
func collectionRepeatedPage(seen map[string]bool, ids []string) bool {
	sort.Strings(ids)
	b, _ := json.Marshal(ids)
	key := problem.HashContent(string(b))
	if seen[key] {
		return true
	}
	seen[key] = true
	return false
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

func (h *Collector) collectLevel(ctx context.Context, c CollectionReader, owner string, t *educoder.Task, p educoder.ChallengeProgress, s *CollectionSummary, receipt *questionbank.CollectionReceipt) error {
	if t == nil || p.ChallengeID != t.Challenge.ID || !p.HasFullScore() {
		s.skip("target_not_confirmed_full_score", true)
		return nil
	}
	switch t.Kind() {
	case educoder.TaskKindProgramming:
		// Terminal-only tasks expose a readme-like placeholder, not the script
		// that changed the runtime. Only the execution adapter can bind such a
		// script to a formal evaluation; never import the placeholder as an answer.
		if t.IsTerminalTask() {
			s.skip("terminal_submitted_script_unobservable", false)
			return nil
		}
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
			if errors.Is(err, errCollectionScoreUnconfirmed) {
				s.skip("score_changed_or_unconfirmed", true)
				return nil
			}
			return err
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
		candidate, candidateErr := h.Bank.FindVerified(ctx, questionbank.Ref{RevisionHash: prepared.RevisionHash, ContractHash: prepared.ContractHash})
		if candidateErr == nil && candidate.FilesHash == hash {
			receipt.CodeCandidateID, receipt.Questions = candidate.ID, 1
		} else if candidateErr != nil && !errors.Is(candidateErr, questionbank.ErrNotFound) {
			return candidateErr
		}
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
			if errors.Is(err, errCollectionScoreUnconfirmed) {
				s.skip("score_changed_or_unconfirmed", true)
				return nil
			}
			return err
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
		// Store the exact context that justified the read-only full-score sheet.
		// Future upgrades can recover context without re-solving or losing IDs.
		scope, err := h.Bank.PutObjectiveSnapshot(ctx, snapshot)
		if err != nil {
			return err
		}
		contextComplete := t.ObjectiveReadComplete() && fresh.ObjectiveReadComplete()
		context := questionbank.ObjectiveQuestionContext{Kind: "objective", Title: snapshot.Title, Statement: snapshot.Statement, Complete: contextComplete}
		if !contextComplete {
			s.skip("objective_context_incomplete", true)
			return nil
		}
		for i, q := range snapshot.Questions {
			key, reusable, err := h.Bank.UpsertObjectiveQuestionWithContext(ctx, questionbank.ObjectiveQuestionInput{Question: q, Context: context})
			if err == nil && !reusable {
				s.skip("objective_context_incomplete", true)
				return nil
			}
			if err != nil {
				return err
			}
			existing, err := h.Bank.LookupObjectiveAnswersWithContext(ctx, key)
			if err != nil {
				return err
			}
			var verifiedAnswerID int64
			for _, a := range existing {
				if a.Reusable && a.Answer == answers[i] {
					verifiedAnswerID = a.ID
					break
				}
			}
			if verifiedAnswerID > 0 {
				if err = h.Bank.RecordObjectiveContribution(ctx, owner, verifiedAnswerID); err != nil {
					return err
				}
				s.Duplicates++
				receipt.ObjectiveAnswerIDs = append(receipt.ObjectiveAnswerIDs, verifiedAnswerID)
				continue
			}
			validation := questionbank.ObjectiveValidation{Passed: true, Confirmed: true, FullScore: true, Score: p.GameScore, MaxScore: p.ChallengeScore, AnswerHash: problem.HashContent(answers[i]), ScopeHash: scope, Feedback: "Read-only collection: exact target full score and complete persisted answer sheet; matching question identities and answers confirmed by two task reads; no explicit conflicting correctness result."}
			id, err := recordObjectiveBankAnswer(ctx, h.Bank, questionbank.ObjectiveQuestionInput{Question: q, Context: context}, answers[i], "platform_collection", validation)
			if err != nil {
				return err
			}
			if err = h.Bank.RecordObjectiveContribution(ctx, owner, id); err != nil {
				return err
			}
			s.Collected++
			receipt.ObjectiveAnswerIDs = append(receipt.ObjectiveAnswerIDs, id)
		}
		receipt.Questions = len(snapshot.Questions)
		return nil
	default:
		s.skip("unsupported_task_kind", false)
		return nil
	}
}

var errCollectionScoreUnconfirmed = errors.New("collection score changed, missing or ambiguous")

func collectionFreshScore(ctx context.Context, c CollectionReader, t *educoder.Task, before educoder.ChallengeProgress) error {
	p, err := c.HomeworkProgress(ctx, t.CourseIdentifier, t.HomeworkID)
	if err != nil {
		return err
	}
	if p == nil {
		return errCollectionScoreUnconfirmed
	}
	count := 0
	for _, v := range p.Challenges {
		if v.ChallengeID == t.Challenge.ID {
			count++
			if !v.HasFullScore() || v.GameScore != before.GameScore || v.ChallengeScore != before.ChallengeScore {
				return errCollectionScoreUnconfirmed
			}
		}
	}
	if count != 1 {
		return errCollectionScoreUnconfirmed
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
	if collectionMedia.MatchString(s.Title) || collectionMedia.MatchString(s.Statement) {
		return s, nil, "objective_media_content_unproven"
	}
	if err := answer.ValidateObjectiveCandidate(s, answer.ObjectiveCandidate{Kind: "objective", Answers: answers}); err != nil {
		return s, nil, "objective_contract_invalid"
	}
	return s, answers, ""
}

func CollectionMessage(s CollectionSummary) string {
	return fmt.Sprintf("已采集 %d，重复 %d，跳过 %d，未确认 %d，失败 %d；检查点复用 %d 关 / %d 题", s.Collected, s.Duplicates, s.Skipped, s.Unconfirmed, s.Failed, s.ReusedLevels, s.ReusedQuestions)
}
