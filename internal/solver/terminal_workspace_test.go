package solver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestTerminalPartialRepairNeverBecomesIndependentSharedAnswer(t *testing.T) {
	for _, scenario := range []string{"earlier_candidate", "earlier_job", "initial_workspace"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			db, err := questionbank.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			first, second, complete := terminalCandidate(), terminalCandidate(), terminalCandidate()
			first.Content, second.Content, complete.Content = "touch A\nfalse\n", "touch B\n", "touch A B\n"
			files := map[string]bool{}
			runs, submits := 0, 0
			newAdapter := func() *terminalProgressAdapter {
				a, _ := terminalTestAdapter(t)
				a.terminalRun = func(_ context.Context, _ *educoder.Task, script string, _ educoder.TerminalOptions) (*educoder.TerminalResult, error) {
					runs++
					result := terminalSuccess(problem.SolutionFile{Path: first.Path, Content: script})
					switch script {
					case first.Content:
						files["A"] = true
						exit := 1
						result.ExitCode, result.Output = &exit, "operation after creating A failed\n"
					case second.Content:
						files["B"] = true
					case complete.Content:
						files["A"], files["B"] = true, true
					default:
						t.Fatalf("unexpected script: %q", script)
					}
					return result, nil
				}
				a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
					submits++
					if !files["A"] || !files["B"] {
						t.Fatal("formal evaluation passed without required workspace state")
					}
					return terminalPass(), nil
				}
				return &terminalProgressAdapter{a}
			}
			candidate := func(file problem.SolutionFile) answer.Candidate {
				return answer.Candidate{Kind: "programming", Language: "shell", Files: []problem.SolutionFile{file}}
			}
			owner := "platform:" + strings.Repeat("a", 32)
			req := Request{Owner: owner, Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit, MaxModelCalls: 2, MaxEvaluations: 2}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate(second)}}
			if scenario == "earlier_candidate" {
				provider.candidates = []answer.Candidate{candidate(first), candidate(second)}
			} else if scenario == "earlier_job" {
				failed := New(db, &fakeProvider{candidates: []answer.Candidate{candidate(first)}}, Config{Timeout: time.Second})
				failedReq := req
				failedReq.Mode, failedReq.MaxModelCalls = ModeSelfTest, 1
				prior, err := failed.Run(ctx, newAdapter(), failedReq)
				if err != nil || prior.Status != "selftest_failed" || !files["A"] {
					t.Fatalf("earlier failed job fixture: %+v %v", prior, err)
				}
			} else {
				files["A"] = true
			}
			adapter := newAdapter()
			service := New(db, provider, Config{Timeout: time.Second})
			result, err := service.Run(ctx, adapter, req)
			if err != nil || result.Status != "passed" || result.Evaluation == nil || !result.Evaluation.Passed || !result.Evaluation.ReuseUnproven || !files["A"] || !files["B"] {
				t.Fatalf("legitimate current workspace pass was lost: %+v %v", result, err)
			}
			loaded, err := adapter.Load(ctx, Target{"c", 1, "g"})
			if err != nil {
				t.Fatal(err)
			}
			snap, err := problem.Prepare(loaded.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			ref := questionbank.Ref{RevisionHash: snap.RevisionHash, ContractHash: snap.ContractHash}
			stored, err := db.Candidate(ctx, result.CandidateID)
			if err != nil || stored.Status != "workspace_passed" || stored.Validations[len(stored.Validations)-1].Kind != "formal_workspace" {
				t.Fatalf("partial C2 was promoted to global proof: %+v %v", stored, err)
			}
			if answer, err := db.FindVerified(ctx, ref); !errors.Is(err, questionbank.ErrNotFound) {
				t.Fatalf("partial C2 was independently reusable: %+v %v", answer, err)
			}
			if summary, err := db.Summary(ctx); err != nil || summary.TotalVerifiedQuestions != 0 {
				t.Fatalf("partial script inflated trusted bank: %+v %v", summary, err)
			}
			if contributed, err := db.ContributedQuestions(ctx, owner); err != nil || contributed != 0 {
				t.Fatalf("partial script inflated contributions: %d %v", contributed, err)
			}
			// A new job/adapter must not execute C2 automatically in a fresh
			// workspace. Generation is required even though C2 once passed.
			files = map[string]bool{}
			freshProvider := &fakeProvider{candidates: []answer.Candidate{candidate(complete)}}
			freshService := New(db, freshProvider, Config{Timeout: time.Second})
			fresh, err := freshService.Run(ctx, newAdapter(), req)
			if err != nil || fresh.Status != "passed" || freshProvider.calls != 1 || fresh.CodeHash == result.CodeHash || !files["A"] || !files["B"] {
				t.Fatalf("fresh job reused state-dependent partial script: %+v err=%v calls=%d", fresh, err, freshProvider.calls)
			}
			wantRuns := 3
			if scenario == "initial_workspace" {
				wantRuns = 2
			}
			if runs != wantRuns || submits != 2 {
				t.Fatalf("unexpected script/formal mutation counts: runs=%d submits=%d", runs, submits)
			}
		})
	}
}
