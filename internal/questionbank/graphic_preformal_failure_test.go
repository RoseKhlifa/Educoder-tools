package questionbank

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func TestPreformalGraphicFailureRequiresBoundSourceAndNeverReplaysUnknownBuild(t *testing.T) {
	for _, scenario := range []string{
		"legacy_hash_fallback", "empty_validation_hashes", "multiple_unknown", "wrong_owner", "wrong_key", "wrong_course", "wrong_homework", "empty_game", "wrong_mode", "state_owner_mismatch", "state_key_mismatch", "wrong_run_id", "running", "result_not_uncertain", "missing_evaluation", "evaluation_saved", "evaluation_submitted", "evaluation_passed", "evaluation_complete", "evaluation_count", "evaluation_status", "nongraphic", "non_mdj", "multiple_snapshot_files", "candidate_path", "multiple_candidate_files", "candidate_hash", "candidate_bytes", "candidate_ref", "missing_candidate", "missing_snapshot", "missing_evidence", "other_run_evidence", "other_candidate_evidence", "known_formal_failure", "known_formal_pass", "formal_workspace", "terminal_evidence", "complete_evidence", "count_evidence", "wrong_validation_hash", "wrong_validation_contract", "too_many_evidence",
	} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			snapshot := problem.Snapshot{Kind: "programming", Title: "graphic fixture", StatementMarkdown: "edit graphic", EditMode: "graphic", Files: []problem.File{{Path: "submit/step7.mdj", Language: "json", CurrentContent: `{"_type":"Project"}`, StarterProvenance: "unknown"}}, EnvironmentContract: []string{"graphic:staruml", "language:json"}}
			if scenario == "nongraphic" {
				snapshot.EditMode = "single_file"
			}
			if scenario == "non_mdj" {
				snapshot.Files[0].Path = "submit/step7.json"
			}
			if scenario == "multiple_snapshot_files" {
				snapshot.Files = append(snapshot.Files, problem.File{Path: "second.mdj", Language: "json", StarterProvenance: "unknown"})
			}
			ref, err := s.UpsertSnapshot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			files := []problem.SolutionFile{{Path: snapshot.Files[0].Path, Content: `{"_type":"Project","_id":"fixture"}`}}
			if scenario == "multiple_snapshot_files" {
				files = append(files, problem.SolutionFile{Path: "second.mdj", Content: "{}"})
			}
			candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: files, Source: "model"})
			if err != nil {
				t.Fatal(err)
			}
			owner, key, id := strings.Repeat("a", 64), "course-key:5547026", "run-graphic-preformal"
			eval := map[string]any{"Status": "error", "SavedFilesHash": "", "SubmittedFilesHash": "", "Completed": false, "Passed": false, "TestCount": 0, "PassedCount": 0}
			result := map[string]any{"run_id": id, "status": "uncertain", "candidate_id": candidate.ID, "code_hash": candidate.FilesHash, "evaluation": eval, "diagnostics": []string{"context deadline exceeded"}}
			target := map[string]any{"Course": "C", "HomeworkID": 42, "Game": "G"}
			state := map[string]any{"owner": owner, "idempotency_key": key, "mode": "submit", "target": target, "snapshot_ref": ref, "result": result}
			runStatus := "uncertain"
			switch scenario {
			case "empty_game":
				target["Game"] = ""
			case "wrong_mode":
				state["mode"] = "generate"
			case "state_owner_mismatch":
				state["owner"] = strings.Repeat("b", 64)
			case "state_key_mismatch":
				state["idempotency_key"] = "another-key"
			case "wrong_run_id":
				result["run_id"] = "another-run"
			case "running":
				runStatus = "running"
			case "result_not_uncertain":
				result["status"] = "failed"
			case "missing_evaluation":
				delete(result, "evaluation")
			case "evaluation_saved":
				eval["SavedFilesHash"] = candidate.FilesHash
			case "evaluation_submitted":
				eval["SubmittedFilesHash"] = candidate.FilesHash
			case "evaluation_passed":
				eval["Passed"] = true
			case "evaluation_complete":
				eval["Completed"] = true
			case "evaluation_count":
				eval["TestCount"] = 1
			case "evaluation_status":
				eval["Status"] = "save_error"
			case "candidate_hash":
				result["code_hash"] = strings.Repeat("b", 64)
			case "missing_candidate":
				result["candidate_id"] = candidate.ID + 99
			case "missing_snapshot":
				state["snapshot_ref"] = Ref{RevisionHash: strings.Repeat("b", 64), ContractHash: ref.ContractHash}
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.PutRun(ctx, RunRecord{ID: id, Owner: owner, IdempotencyKey: key, Status: runStatus, Data: raw, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			validation := Validation{CandidateID: candidate.ID, Kind: "formal", Result: "unknown", FilesHash: candidate.FilesHash, SavedFilesHash: candidate.FilesHash, SubmittedFilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: id}
			switch scenario {
			case "empty_validation_hashes":
				validation.SavedFilesHash, validation.SubmittedFilesHash = "", ""
			case "other_run_evidence":
				validation.RunID = "another-run"
			case "other_candidate_evidence":
				other, addErr := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: files[0].Path, Content: "{}"}}, Source: "model"})
				if addErr != nil {
					t.Fatal(addErr)
				}
				validation.CandidateID, validation.FilesHash, validation.SavedFilesHash, validation.SubmittedFilesHash = other.ID, other.FilesHash, other.FilesHash, other.FilesHash
			case "known_formal_failure":
				validation.Result = "failed"
			case "known_formal_pass":
				validation.Result, validation.Terminal, validation.Complete, validation.TestCount, validation.PassedCount = "passed", true, true, 1, 1
			case "formal_workspace":
				validation.Kind = "formal_workspace"
			case "terminal_evidence":
				validation.Terminal = true
			case "complete_evidence":
				validation.Complete = true
			case "count_evidence":
				validation.TestCount = 1
			}
			if scenario != "missing_evidence" {
				count := 1
				if scenario == "multiple_unknown" {
					count = 2
				}
				if scenario == "too_many_evidence" {
					count = 65
				}
				for i := 0; i < count; i++ {
					if err = s.RecordValidation(ctx, validation); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Corruption must fail closed even when the normal writer would reject it.
			if scenario == "candidate_path" {
				if _, err = s.db.ExecContext(ctx, "UPDATE solution_files SET path=? WHERE solution_id=?", "other.mdj", candidate.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "multiple_candidate_files" {
				if _, err = s.db.ExecContext(ctx, "INSERT INTO solution_files(solution_id,path,content,content_hash) VALUES(?,?,?,?)", candidate.ID, "second.mdj", "{}", problem.HashContent("{}")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "candidate_bytes" {
				if _, err = s.db.ExecContext(ctx, "UPDATE solution_files SET content=? WHERE solution_id=?", "changed bytes", candidate.ID); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "candidate_ref" {
				otherSnapshot := snapshot
				otherSnapshot.EnvironmentContract = []string{"different-contract"}
				otherRef, putErr := s.UpsertSnapshot(ctx, otherSnapshot)
				if putErr != nil {
					t.Fatal(putErr)
				}
				state["snapshot_ref"] = otherRef
				raw, _ = json.Marshal(state)
				if err = s.PutRun(ctx, RunRecord{ID: id, Owner: owner, IdempotencyKey: key, Status: runStatus, Data: raw, CreatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong_validation_hash" || scenario == "wrong_validation_contract" {
				if scenario == "wrong_validation_hash" {
					validation.FilesHash = strings.Repeat("b", 64)
				} else {
					validation.ContractHash = strings.Repeat("b", 64)
				}
				if _, err = s.db.ExecContext(ctx, "UPDATE verifications SET evidence_json=?", marshal(validation)); err != nil {
					t.Fatal(err)
				}
			}
			queryOwner, queryKey, course, homework := owner, key, "C", int64(42)
			switch scenario {
			case "wrong_owner":
				queryOwner = strings.Repeat("c", 64)
			case "wrong_key":
				queryKey = "another-key"
			case "wrong_course":
				course = "OTHER"
			case "wrong_homework":
				homework = 99
			}
			got, err := s.PreformalGraphicFailure(ctx, queryOwner, queryKey, course, homework)
			want := scenario == "legacy_hash_fallback" || scenario == "empty_validation_hashes" || scenario == "multiple_unknown"
			if err != nil || got != want {
				t.Fatalf("allow=%v want=%v err=%v", got, want, err)
			}
			stored, err := s.GetRun(ctx, id)
			if err != nil || string(stored.Data) != string(raw) {
				t.Fatal("proof changed original run")
			}
		})
	}
}
