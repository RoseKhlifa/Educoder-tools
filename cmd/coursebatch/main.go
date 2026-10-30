// coursebatch executes an explicit manifest of previously inventoried tasks.
// Credentials and runtime reports are supplied outside the source tree.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

type settings struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
}

type target struct {
	HomeworkID   int64             `json:"homework_id"`
	HomeworkName string            `json:"homework_name"`
	Game         string            `json:"game"`
	ChallengeID  int64             `json:"challenge_id"`
	Name         string            `json:"name"`
	Kind         educoder.TaskKind `json:"kind"`
	Max          string            `json:"max"`
	InitialScore string            `json:"initial_score"`
}

type outcome struct {
	Target       target    `json:"target"`
	State        string    `json:"state"`
	Error        string    `json:"error,omitempty"`
	Score        string    `json:"score,omitempty"`
	MaxScore     string    `json:"max_score,omitempty"`
	ModelCalls   int       `json:"model_calls"`
	CandidateIDs []int64   `json:"candidate_ids,omitempty"`
	Answers      []string  `json:"answers,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type auditTransport struct {
	base http.RoundTripper
	file string
	mu   sync.Mutex
}

func (a *auditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := a.base.RoundTrip(req)
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		return resp, err
	}
	row := map[string]any{"at": started.UTC(), "duration_ms": time.Since(started).Milliseconds()}
	if err != nil {
		row["error"] = "transport_failed"
	} else {
		row["http_status"] = resp.StatusCode
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		var data struct {
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Usage   json.RawMessage `json:"usage"`
			Choices []struct {
				Finish string `json:"finish_reason"`
			} `json:"choices"`
		}
		if readErr == nil && json.Unmarshal(body, &data) == nil {
			row["request_id"] = data.ID
			row["model"] = data.Model
			if len(data.Usage) > 0 {
				row["usage"] = data.Usage
			}
			if len(data.Choices) > 0 {
				row["finish_reason"] = data.Choices[0].Finish
			}
		}
	}
	row["duration_ms"] = time.Since(started).Milliseconds()
	a.mu.Lock()
	defer a.mu.Unlock()
	f, openErr := os.OpenFile(a.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if openErr != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, errors.New("model usage audit unavailable")
	}
	defer f.Close()
	b, _ := json.Marshal(row)
	_, writeErr := f.Write(append(b, '\n'))
	if writeErr != nil {
		return nil, errors.New("model usage audit failed")
	}
	return resp, err
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "local credentials file")
	manifestPath := flag.String("targets", "", "explicit target manifest")
	bankPath := flag.String("bank", "", "SQLite question bank")
	outPath := flag.String("out", "", "local report directory")
	course := flag.String("course", "", "selected classroom identifier")
	kind := flag.String("kind", "all", "all, programming or choice")
	onlyID := flag.Int64("challenge", 0, "optional single target challenge")
	reconcileID := flag.String("reconcile", "", "read-only reconciliation of a saved uncertain run")
	importAudit := flag.Bool("import-usage", false, "persist this completed batch's HTTP usage ledger and balance report")
	manualPath := flag.String("candidate", "", "reviewed objective candidate for a single explicit challenge")
	manualSource := flag.String("source", "", "reviewed programming source for a single explicit challenge")
	flag.Parse()
	if *configPath == "" || *manifestPath == "" || *bankPath == "" || *outPath == "" || *course == "" {
		return errors.New("config, targets, bank, out and course are required")
	}
	if err := os.MkdirAll(*outPath, 0700); err != nil {
		return err
	}
	var cfg settings
	b, err := os.ReadFile(*configPath)
	if err != nil {
		return errors.New("local credentials unavailable")
	}
	if json.Unmarshal(b, &cfg) != nil {
		return errors.New("invalid credentials configuration")
	}
	var targets []target
	b, err = os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &targets) != nil {
		return errors.New("invalid target manifest")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	client := educoder.New(educoder.DefaultBaseURL)
	user, err := client.Login(ctx, cfg.Login, cfg.Password)
	cfg.Password = ""
	if err != nil {
		return errors.New("account login failed")
	}
	current, err := client.UserInfo(ctx, "")
	if err != nil || current.Login != user.Login {
		return errors.New("account identity verification failed")
	}
	store, err := questionbank.Open(*bankPath)
	if err != nil {
		return err
	}
	defer store.Close()
	audit := &auditTransport{base: http.DefaultTransport, file: filepath.Join(*outPath, "model-usage.jsonl")}
	provider, err := answer.NewDeepSeek(answer.Config{APIKey: cfg.APIKey, HTTPClient: &http.Client{Transport: audit}, Timeout: 2 * time.Minute})
	cfg.APIKey = ""
	if err != nil {
		return err
	}
	repairProfile := answer.DefaultProfile()
	repairProfile.MaxTokens = 8192
	proProfile := answer.ProProfile()
	proProfile.Thinking, proProfile.ReasoningEffort, proProfile.MaxTokens = "disabled", "", 8192
	service := solver.New(store, provider, solver.Config{Timeout: 6 * time.Minute, MaxModelCalls: 6, MaxEvaluations: 6, BudgetMicroUSD: 200000, RepairProfile: repairProfile, ProProfile: proProfile})
	if *importAudit {
		return importUsageAudit(ctx, store, user.Login, *outPath)
	}
	if *reconcileID != "" {
		r, e := service.ReconcileClient(ctx, client, user.Login, *reconcileID)
		if r != nil {
			writeJSON(filepath.Join(*outPath, "reconciled-"+*reconcileID+".json"), r)
			fmt.Printf("RECONCILE state=%s candidate=%d model_calls_added=0\n", r.Status, r.CandidateID)
		}
		return e
	}
	if balance, e := provider.ReadBalance(ctx); e == nil {
		writeJSON(filepath.Join(*outPath, "balance-run-start-"+*kind+".json"), balance)
	}
	failures := 0
	for _, t := range targets {
		if *kind != "all" && string(t.Kind) != *kind {
			continue
		}
		if *onlyID != 0 && t.ChallengeID != *onlyID {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result := outcome{Target: t, State: "running", UpdatedAt: time.Now().UTC()}
		file := filepath.Join(*outPath, fmt.Sprintf("result-%d.json", t.ChallengeID))
		fmt.Printf("START challenge=%d kind=%s name=%s\n", t.ChallengeID, t.Kind, t.Name)
		level, e := progressFor(ctx, client, *course, t)
		if e != nil {
			result.State = "progress_error"
			result.Error = "initial score unavailable"
			writeJSON(file, result)
			failures++
			continue
		}
		if level.HasFullScore() {
			result.State = "already_full_score"
			result.Score = level.GameScore
			result.MaxScore = level.ChallengeScore
			writeJSON(file, result)
			fmt.Printf("SKIP challenge=%d score=%s/%s\n", t.ChallengeID, level.GameScore, level.ChallengeScore)
			continue
		}
		if t.Kind == educoder.TaskKindProgramming {
			if *manualSource != "" {
				if *onlyID == 0 {
					return errors.New("reviewed source requires one explicit challenge")
				}
				adapter, e := solver.NewEducoderAdapter(client)
				if e != nil {
					return e
				}
				loaded, e := adapter.Load(ctx, solver.Target{Course: *course, HomeworkID: t.HomeworkID, Game: t.Game})
				if e != nil {
					return e
				}
				if len(loaded.Snapshot.Files) != 1 {
					return errors.New("reviewed source requires one file")
				}
				ref, e := store.UpsertSnapshot(ctx, loaded.Snapshot)
				if e != nil {
					return e
				}
				source, e := os.ReadFile(*manualSource)
				if e != nil {
					return e
				}
				// The observed editor save converts CRLF to LF. Establish the
				// exact submitted candidate before hashing or recording evidence.
				submittedSource := strings.ReplaceAll(string(source), "\r\n", "\n")
				candidate, e := store.AddCandidate(ctx, ref, questionbank.CandidateInput{Files: []problem.SolutionFile{{Path: loaded.Snapshot.Files[0].Path, Content: submittedSource}}, Source: "reviewed_repair", Model: "review", PromptVersion: "review-v1"})
				if e != nil {
					return e
				}
				result.CandidateIDs = []int64{candidate.ID}
				err = submitReviewed(ctx, client, store, *course, t, candidate, *outPath, &result)
				if err != nil {
					result.Error = err.Error()
				}
			} else {
				r, e := service.RunClient(ctx, client, solver.Request{Owner: user.Login, Course: *course, HomeworkID: t.HomeworkID, Game: t.Game, Mode: solver.ModeSubmit, Timeout: 6 * time.Minute, MaxModelCalls: 6, MaxEvaluations: 6, BudgetMicroUSD: 200000})
				if r != nil {
					result.RunID = r.RunID
					result.ModelCalls = r.ModelCalls
					result.CandidateIDs = []int64{r.CandidateID}
					result.State = r.Status
					writeJSON(filepath.Join(*outPath, fmt.Sprintf("programming-%d.json", t.ChallengeID)), r)
				}
				if e != nil {
					result.Error = e.Error()
				}
			}
		} else if t.Kind == educoder.TaskKindChoice {
			var reviewed *answer.ObjectiveCandidate
			if *manualPath != "" {
				if *onlyID == 0 {
					return errors.New("a reviewed candidate requires one explicit challenge")
				}
				data, readErr := os.ReadFile(*manualPath)
				if readErr != nil {
					return readErr
				}
				reviewed = &answer.ObjectiveCandidate{}
				if json.Unmarshal(data, reviewed) != nil {
					return errors.New("reviewed objective candidate is invalid")
				}
			}
			err = solveChoice(ctx, client, provider, store, *course, t, *outPath, &result, reviewed)
			if err != nil {
				result.Error = err.Error()
			}
		} else {
			result.State = "unsupported_type"
			result.Error = "unobserved task kind"
		}
		level, e = progressFor(ctx, client, *course, t)
		if e == nil {
			result.Score = level.GameScore
			result.MaxScore = level.ChallengeScore
			if level.HasFullScore() {
				result.State = "full_score"
			}
		}
		result.UpdatedAt = time.Now().UTC()
		writeJSON(file, result)
		fmt.Printf("END challenge=%d state=%s score=%s/%s model_calls=%d\n", t.ChallengeID, result.State, result.Score, result.MaxScore, result.ModelCalls)
		if result.State != "full_score" {
			failures++
		}
	}
	if balance, e := provider.ReadBalance(ctx); e == nil {
		writeJSON(filepath.Join(*outPath, "balance-run-end-"+*kind+".json"), balance)
	}
	fmt.Printf("BATCH_FINISHED failures=%d\n", failures)
	if failures > 0 {
		return errors.New("unfinished targets retained for repair")
	}
	return nil
}

func progressFor(ctx context.Context, c *educoder.Client, course string, t target) (educoder.ChallengeProgress, error) {
	p, err := c.HomeworkProgress(ctx, course, t.HomeworkID)
	if err != nil {
		return educoder.ChallengeProgress{}, err
	}
	for _, x := range p.Challenges {
		if x.ChallengeID == t.ChallengeID {
			return x, nil
		}
	}
	return educoder.ChallengeProgress{}, errors.New("target missing from progress")
}

// submitReviewed accepts an explicitly selected source that was reviewed with
// its repository caller. It records sample observations separately from the
// formal verdict; template-generated whitespace is never labelled sample-pass.
func submitReviewed(ctx context.Context, c *educoder.Client, store *questionbank.Store, course string, t target, candidate questionbank.Candidate, out string, result *outcome) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	task, err := c.LoadTask(ctx, course, t.HomeworkID, t.Game)
	if err != nil {
		return err
	}
	if len(candidate.Files) != 1 {
		return errors.New("reviewed execution requires one source")
	}
	file := candidate.Files[0]
	result.RunID = fmt.Sprintf("review-%d-%d", t.ChallengeID, time.Now().UnixNano())
	for i, test := range task.TestSets {
		if !test.IsPublic {
			continue
		}
		expected := test.Output
		observed, e := c.SelfTest(ctx, task, file.Path, file.Content, test.Input, educoder.SelfTestOptions{Timeout: 45 * time.Second, ExpectedOutput: &expected})
		writeJSON(filepath.Join(out, fmt.Sprintf("review-selftest-%d-%d.json", t.ChallengeID, i)), observed)
		matched := e == nil && observed != nil && observed.Completed && observed.Matches != nil && *observed.Matches
		state := "failed"
		if matched {
			state = "passed"
		}
		feedback := "reviewed source public sample observation"
		if e != nil {
			state = "unknown"
			feedback = e.Error()
		}
		if err := store.RecordValidation(ctx, questionbank.Validation{CandidateID: candidate.ID, Kind: "selftest", Result: state, FilesHash: candidate.FilesHash, RevisionHash: candidate.RevisionHash, ContractHash: candidate.ContractHash, RunID: result.RunID, Terminal: e == nil, Complete: observed != nil && observed.Completed, TestCount: 1, PassedCount: boolInt(matched), Feedback: feedback}); err != nil {
			return err
		}
	}
	saved, err := c.SaveCode(ctx, task, file.Path, file.Content, true)
	if err != nil {
		return err
	}
	actual, err := c.ReadCodeFile(ctx, task, file.Path)
	if err != nil {
		return err
	}
	hash, _ := problem.HashFiles([]problem.SolutionFile{{Path: file.Path, Content: actual.Content}})
	if hash != candidate.FilesHash {
		return errors.New("saved source differs from reviewed candidate")
	}
	if _, err = c.BuildCode(ctx, task, saved); err != nil {
		return err
	}
	evaluation, pollErr := c.WaitEvaluation(ctx, task, saved, educoder.PollOptions{Timeout: 45 * time.Second})
	if evaluation != nil {
		writeJSON(filepath.Join(out, fmt.Sprintf("review-evaluation-%d.json", t.ChallengeID)), evaluation)
	}
	var level educoder.ChallengeProgress
	var fresh *educoder.Task
	confirmed := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		fresh, err = c.LoadTask(ctx, course, t.HomeworkID, t.Game)
		if err == nil && fresh.Game.Status == 2 && len(fresh.TestSets) > 0 {
			all := true
			for _, test := range fresh.TestSets {
				if test.Result == nil || !*test.Result || test.CompileSuccess == nil || *test.CompileSuccess != 1 {
					all = false
				}
			}
			if all {
				level, err = progressFor(ctx, c, course, t)
				if err == nil && level.HasFullScore() {
					confirmed = true
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	actual, err = c.ReadCodeFile(ctx, task, file.Path)
	if err != nil {
		return err
	}
	endHash, _ := problem.HashFiles([]problem.SolutionFile{{Path: file.Path, Content: actual.Content}})
	if endHash != candidate.FilesHash {
		confirmed = false
	}
	count, passed := 0, 0
	if fresh != nil {
		count = len(fresh.TestSets)
		for _, test := range fresh.TestSets {
			if test.Result != nil && *test.Result && test.CompileSuccess != nil && *test.CompileSuccess == 1 {
				passed++
			}
		}
	}
	validation := "failed"
	if confirmed {
		validation = "passed"
	}
	feedback := "read-only verification after reviewed source submission"
	if pollErr != nil {
		feedback += "; initial polling did not provide a classified terminal result"
	}
	if err := store.RecordValidation(ctx, questionbank.Validation{CandidateID: candidate.ID, Kind: "formal", Result: validation, FilesHash: candidate.FilesHash, SavedFilesHash: hash, SubmittedFilesHash: endHash, RevisionHash: candidate.RevisionHash, ContractHash: candidate.ContractHash, RunID: result.RunID, Terminal: confirmed, Complete: confirmed, TestCount: count, PassedCount: passed, Feedback: feedback}); err != nil {
		return err
	}
	result.State = "review_failed"
	if confirmed {
		result.State = "full_score"
		result.Score = level.GameScore
		result.MaxScore = level.ChallengeScore
		return nil
	}
	return errors.New("reviewed source has not reached a confirmed full score")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func solveChoice(ctx context.Context, c *educoder.Client, p *answer.DeepSeek, store *questionbank.Store, course string, t target, out string, result *outcome, reviewed *answer.ObjectiveCandidate) error {
	task, err := c.LoadChoiceTask(ctx, course, t.HomeworkID, t.Game)
	if err != nil {
		result.State = "load_error"
		return err
	}
	snapshot := answer.ObjectiveSnapshot{Kind: "objective", Title: task.Task.Challenge.Subject, Statement: task.Task.Challenge.Description}
	resources := []answer.ImageResource{}
	keys := []string{}
	cached := []string{}
	for _, q := range task.Questions {
		question := answer.ObjectiveQuestion{Position: q.Position, Question: q.Subject, QuestionType: q.QuestionType, Category: q.Category}
		bankResources := []questionbank.ObjectiveResource{}
		media, refs, e := loadImages(ctx, c, q.Position, "", q.Subject, out)
		if e != nil {
			return e
		}
		resources = append(resources, media...)
		bankResources = append(bankResources, refs...)
		for _, o := range q.Options {
			label := string(rune('A' + o.Position))
			question.Options = append(question.Options, answer.ObjectiveOption{Label: label, Text: o.OptionName, Position: o.Position})
			media, refs, e = loadImages(ctx, c, q.Position, label, o.OptionName, out)
			if e != nil {
				return e
			}
			resources = append(resources, media...)
			bankResources = append(bankResources, refs...)
		}
		snapshot.Questions = append(snapshot.Questions, question)
		key, e := store.UpsertObjectiveQuestionWithResources(ctx, question, bankResources)
		if e != nil {
			return e
		}
		keys = append(keys, key)
		answers, e := store.LookupObjectiveAnswers(ctx, key)
		if e != nil {
			return e
		}
		selected := ""
		for _, a := range answers {
			if a.Reusable {
				selected = a.Answer
				break
			}
		}
		cached = append(cached, selected)
	}
	writeJSON(filepath.Join(out, fmt.Sprintf("objective-snapshot-%d.json", t.ChallengeID)), snapshot)
	var previous *answer.ObjectiveCandidate
	feedback := []answer.Feedback{}
	for attempt := 1; attempt <= 6; attempt++ {
		candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: append([]string(nil), cached...)}
		missing := false
		for _, a := range candidate.Answers {
			if a == "" {
				missing = true
			}
		}
		model := "bank"
		source := "question_bank"
		if reviewed != nil && attempt == 1 {
			candidate = *reviewed
			source = "reviewed_repair"
			model = "review"
		} else if missing || previous != nil {
			profile := answer.DefaultProfile()
			profile.MaxTokens = 2048
			if attempt > 1 {
				profile = answer.DefaultProfile()
				profile.MaxTokens = 4096
			}
			if attempt > 2 {
				profile = answer.ProProfile()
				profile.Thinking, profile.ReasoningEffort = "disabled", ""
				profile.MaxTokens = 8192
			}
			req := answer.ObjectiveRequest{Problem: snapshot, Previous: previous, Feedback: feedback, Profile: profile, Resources: resources}
			estimate, e := p.EstimateObjective(req)
			if e != nil {
				return e
			}
			if estimate.CostMicroUSD > 200000 {
				return errors.New("single objective call exceeds reserved budget")
			}
			generated, e := p.GenerateObjective(ctx, req)
			result.ModelCalls++
			if e != nil {
				result.State = "model_error"
				return e
			}
			candidate = generated.Candidate
			model = generated.Model
			source = "model"
			// A verified single-question answer remains fixed while the model
			// supplies only missing answers, unless that answer fails this scope.
			if previous == nil {
				for i, a := range cached {
					if a != "" {
						candidate.Answers[i] = a
					}
				}
			}
		}
		if err := answer.ValidateObjectiveCandidate(snapshot, candidate); err != nil {
			return err
		}
		ids := make([]int64, len(keys))
		for i, key := range keys {
			id, e := store.AddObjectiveAnswer(ctx, key, candidate.Answers[i], source, model)
			if e != nil {
				return e
			}
			ids[i] = id
		}
		result.CandidateIDs = ids
		result.Answers = candidate.Answers
		result.State = "submitting"
		writeJSON(filepath.Join(out, fmt.Sprintf("objective-candidate-%d-%d.json", t.ChallengeID, attempt)), candidate)
		evaluation, e := c.ObjectiveSubmit(ctx, task, candidate.Answers)
		if e != nil {
			result.State = "submit_uncertain"
			return e
		}
		writeJSON(filepath.Join(out, fmt.Sprintf("objective-evaluation-%d-%d.json", t.ChallengeID, attempt)), evaluation)
		actualMatches := len(evaluation.TestSets) == len(candidate.Answers)
		for i, ts := range evaluation.TestSets {
			if i >= len(candidate.Answers) {
				actualMatches = false
				break
			}
			if actual, ok := ts.ActualOutput.(string); !ok || actual != candidate.Answers[i] {
				actualMatches = false
			}
		}
		full := evaluation.Passed && evaluation.ScoreKnown && actualMatches
		scope, _ := json.Marshal(struct {
			Keys    []string
			Answers []string
		}{keys, candidate.Answers})
		scopeHash := problem.HashContent(string(scope))
		for i, id := range ids {
			correct := false
			known := false
			if i < len(evaluation.TestSets) && evaluation.TestSets[i].Result != nil {
				known = true
				correct = *evaluation.TestSets[i].Result
			}
			v := questionbank.ObjectiveValidation{Passed: full || correct, Confirmed: full || known, FullScore: full, PerQuestionCorrect: known && correct, Score: evaluation.Score, MaxScore: evaluation.MaxScore, AnswerHash: problem.HashContent(candidate.Answers[i]), ScopeHash: scopeHash, Feedback: fmt.Sprintf("attempt %d; full_score=%t; score=%s/%s", attempt, full, evaluation.Score, evaluation.MaxScore)}
			if err := store.RecordObjectiveValidation(ctx, id, v); err != nil {
				return err
			}
		}
		result.Score = evaluation.Score
		result.MaxScore = evaluation.MaxScore
		if full {
			result.State = "full_score"
			return nil
		}
		result.State = "objective_repair_required"
		feedback = []answer.Feedback{{Kind: "formal", Message: fmt.Sprintf("Your answers earned %s of %s. All questions must be correct. Re-evaluate each answer, preserve answers with an explicit true result, and inspect the exact formulas in all images. Previous submission was not full score.", evaluation.Score, evaluation.MaxScore)}}
		for i, ts := range evaluation.TestSets {
			if ts.Result != nil {
				feedback = append(feedback, answer.Feedback{Kind: "question_result", Message: fmt.Sprintf("Question %d selected %s correct=%t", i+1, candidate.Answers[i], *ts.Result)})
			}
		}
		previous = &candidate
	}
	return errors.New("objective attempts retained for further repair")
}

var markdownImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?([^\s)>]+)`)
var htmlImage = regexp.MustCompile(`(?i)<img\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)

func loadImages(ctx context.Context, c *educoder.Client, position int, label, text, out string) ([]answer.ImageResource, []questionbank.ObjectiveResource, error) {
	locators := []string{}
	for _, re := range []*regexp.Regexp{markdownImage, htmlImage} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			locators = append(locators, m[1])
		}
	}
	resources := []answer.ImageResource{}
	refs := []questionbank.ObjectiveResource{}
	seen := map[string]bool{}
	for _, loc := range locators {
		if seen[loc] {
			continue
		}
		seen[loc] = true
		u, err := url.Parse(loc)
		if err != nil {
			return nil, nil, errors.New("invalid question image locator")
		}
		base, _ := url.Parse(c.BaseURL)
		u = base.ResolveReference(u)
		if u.Host != base.Host || !strings.HasPrefix(u.Path, "/api/attachments/") {
			return nil, nil, errors.New("question image requires an explicit resource adapter")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, errors.New("invalid image request")
		}
		stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		req.Header.Set("Origin", c.Origin)
		req.Header.Set("Referer", c.Origin+"/")
		req.Header.Set("X-EDU-Type", "pc")
		req.Header.Set("X-EDU-Timestamp", stamp)
		req.Header.Set("X-EDU-Signature", educoder.Signature("GET", stamp))
		for _, cookie := range c.HTTP.Jar.Cookies(u) {
			if cookie.Name == "_educoder_session" {
				req.Header.Set("Pc-Authorization", cookie.Value)
			}
		}
		httpClient := *c.HTTP
		httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, nil, errors.New("question image fetch failed")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location, err := resp.Location()
			resp.Body.Close()
			if err != nil || location.Scheme != "https" {
				return nil, nil, errors.New("invalid image redirect")
			}
			imageClient := &http.Client{Timeout: 45 * time.Second}
			r, _ := http.NewRequestWithContext(ctx, "GET", location.String(), nil)
			resp, err = imageClient.Do(r)
			if err != nil {
				return nil, nil, errors.New("image content fetch failed")
			}
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(content) > 10<<20 {
			return nil, nil, errors.New("question image content unavailable")
		}
		mediaType := http.DetectContentType(content)
		hash := problem.HashContent(string(content))
		extension := ".png"
		if mediaType == "image/jpeg" {
			extension = ".jpg"
		}
		if err := os.WriteFile(filepath.Join(out, "image-"+hash+extension), content, 0600); err != nil {
			return nil, nil, err
		}
		resources = append(resources, answer.ImageResource{QuestionPosition: position, OptionLabel: label, MediaType: mediaType, Content: content, SHA256: hash})
		refs = append(refs, questionbank.ObjectiveResource{Locator: loc, SHA256: hash})
	}
	return resources, refs, nil
}
