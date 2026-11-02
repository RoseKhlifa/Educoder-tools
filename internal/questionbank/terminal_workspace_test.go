package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func terminalBankSnapshot() problem.Snapshot {
	return problem.Snapshot{Kind: "programming", Title: "Create A and B", StatementMarkdown: "Create experiment files A and B.", Files: []problem.File{{Path: "placeholder.py", Language: "shell", CurrentContent: ""}}, PublicTests: []problem.PublicTest{{}}, EditMode: "terminal", EnvironmentContract: []string{"terminal:posix-shell/v1", "execution:workspace-state"}}
}

func terminalWorkspaceProof(c Candidate, run string) Validation {
	return Validation{CandidateID: c.ID, Kind: "formal_workspace", Result: "passed", RunID: run, FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}
}

func TestTerminalWorkspaceEvidenceKeepsServiceCompletionWithoutGlobalReuse(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref, err := db.UpsertSnapshot(ctx, terminalBankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	owner := "platform:" + strings.Repeat("a", 32)
	ownerHash := problem.HashContent(owner)
	stamp := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	for _, kind := range []string{"formal_workspace", "formal"} {
		candidate, err := db.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "placeholder.py", Content: "touch B # " + kind + "\n"}}, Source: "model"})
		if err != nil {
			t.Fatal(err)
		}
		runID := "workspace-" + kind
		rawRun, _ := json.Marshal(map[string]any{"mode": "submit", "target": completionTarget{Course: "COURSE", HomeworkID: 1, Game: kind}, "result": map[string]any{"run_id": runID, "status": "passed", "candidate_id": candidate.ID}})
		if err := db.PutRun(ctx, RunRecord{ID: runID, Owner: ownerHash, Status: "passed", Data: rawRun}); err != nil {
			t.Fatal(err)
		}
		proof := terminalWorkspaceProof(candidate, runID)
		proof.Kind = kind
		if kind == "formal" {
			// Simulate a pre-fix release. Existing append-only rows must not
			// require a destructive database rewrite to withdraw global reuse.
			if _, err := db.db.ExecContext(ctx, `INSERT INTO verifications(solution_id,evidence_json,created_at) VALUES(?,?,?)`, candidate.ID, marshal(proof), stamp); err != nil {
				t.Fatal(err)
			}
		} else if err := db.RecordValidation(ctx, proof); err != nil {
			t.Fatal(err)
		}
		loaded, err := db.Candidate(ctx, candidate.ID)
		if err != nil || loaded.Status != "workspace_passed" {
			t.Fatalf("workspace evidence status=%+v err=%v", loaded, err)
		}
	}
	if candidate, err := db.FindVerified(ctx, ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("workspace-only candidate became reusable: %+v %v", candidate, err)
	}
	if summary, err := db.Summary(ctx); err != nil || summary.TotalVerifiedQuestions != 0 {
		t.Fatalf("terminal workspace inflated trusted bank: %+v %v", summary, err)
	}
	if summary, err := db.InventorySummary(ctx); err != nil || summary.TotalQuestions != 0 {
		t.Fatalf("terminal workspace inflated public inventory: %+v %v", summary, err)
	}
	if count, err := db.ContributedQuestions(ctx, owner); err != nil || count != 0 {
		t.Fatalf("workspace script counted as reusable contribution: %d %v", count, err)
	}
	completed, err := db.TodaySummary(ctx, time.Now(), map[string]struct{}{ownerHash: {}})
	if err != nil || completed.CompletedStudents != 1 || completed.CompletedQuestions != 2 || completed.TotalCompletedQuestions != 2 {
		t.Fatalf("real workspace completions lost: %+v %v", completed, err)
	}
}

func TestTerminalFormalEvidenceIsConservativelyDowngradedAndStillRequiresFullProof(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ref, err := db.UpsertSnapshot(ctx, terminalBankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := db.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "placeholder.py", Content: "touch B\n"}}, Source: "model"})
	if err != nil {
		t.Fatal(err)
	}
	proof := terminalWorkspaceProof(candidate, "workspace-run")
	proof.Kind = "formal"
	if err := db.RecordValidation(ctx, proof); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.Candidate(ctx, candidate.ID)
	if err != nil || loaded.Status != "workspace_passed" || len(loaded.Validations) != 1 || loaded.Validations[0].Kind != "formal_workspace" {
		t.Fatalf("terminal formal evidence was not downgraded: %+v %v", loaded, err)
	}
	for _, kind := range []string{"formal", "formal_workspace"} {
		bad := proof
		bad.Kind, bad.Complete = kind, false
		if err := db.RecordValidation(ctx, bad); err == nil {
			t.Fatalf("incomplete %s proof was accepted", kind)
		}
		bad.Complete, bad.SubmittedFilesHash = true, "wrong-candidate"
		if err := db.RecordValidation(ctx, bad); err == nil {
			t.Fatalf("unbound %s proof was accepted", kind)
		}
	}
}
