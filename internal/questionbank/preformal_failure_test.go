package questionbank

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func TestPreformalProgrammingFailureRequiresExactOwnerAndNoFormalAttempt(t *testing.T) {
	for _, scenario := range []string{"eligible", "wrong_owner", "wrong_target", "wrong_key", "terminal", "missing_selftest", "formal_attempt", "formal_workspace", "evaluation_present", "wrong_hash", "nonterminal_run"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			snapshot := bankSnapshot()
			snapshot.EditMode = "single_file"
			if scenario == "terminal" {
				snapshot.EditMode = "terminal"
			}
			ref, err := s.UpsertSnapshot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "answer"}}, Source: "model"})
			if err != nil {
				t.Fatal(err)
			}
			owner, key, id := strings.Repeat("a", 64), "course-key:123", "run-preformal"
			result := map[string]any{"run_id": id, "status": "uncertain", "candidate_id": candidate.ID, "code_hash": candidate.FilesHash, "self_test_status": "unknown"}
			if scenario == "evaluation_present" {
				result["evaluation"] = map[string]any{"Status": "error"}
			}
			if scenario == "wrong_hash" {
				result["code_hash"] = strings.Repeat("b", 64)
			}
			raw, _ := json.Marshal(map[string]any{"owner": owner, "target": map[string]any{"Course": "C", "HomeworkID": 42, "Game": "G"}, "mode": "submit", "snapshot_ref": ref, "result": result})
			status := "uncertain"
			if scenario == "nonterminal_run" {
				status = "running"
			}
			if err = s.PutRun(ctx, RunRecord{ID: id, Owner: owner, IdempotencyKey: key, Status: status, Data: raw, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing_selftest" {
				if err = s.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: "selftest", Result: "unknown", FilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: id}); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(scenario, "formal_") {
				kind := "formal"
				if scenario == "formal_workspace" {
					kind = "formal_workspace"
				}
				if err = s.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: kind, Result: "unknown", FilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: id}); err != nil {
					t.Fatal(err)
				}
			}
			queryOwner, queryKey, course := owner, key, "C"
			if scenario == "wrong_owner" {
				queryOwner = strings.Repeat("c", 64)
			}
			if scenario == "wrong_key" {
				queryKey = "different"
			}
			if scenario == "wrong_target" {
				course = "OTHER"
			}
			got, err := s.PreformalProgrammingFailure(ctx, queryOwner, queryKey, course, 42)
			if err != nil || got != (scenario == "eligible") {
				t.Fatalf("allow=%v err=%v", got, err)
			}
			stored, err := s.GetRun(ctx, id)
			if err != nil || string(stored.Data) != string(raw) {
				t.Fatal("proof changed original run")
			}
		})
	}
}
