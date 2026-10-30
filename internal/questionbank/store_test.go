package questionbank

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func bankSnapshot() problem.Snapshot {
	return problem.Snapshot{Kind: "programming", Title: "Queue", StatementMarkdown: "Write queue", Files: []problem.File{{Path: "src/main.cpp", Language: "cpp", CurrentContent: "template", StarterProvenance: "unknown"}}, PublicTests: []problem.PublicTest{{Stdin: "1\n", ExpectedStdout: "1\n"}}}
}

func TestUpsertSnapshotPreservesCurrentEditorBytesAndHash(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "editor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := bankSnapshot()
	contents := []string{"// 初始代码\r\nint main() { return 0; }\r\n", "// 修改后的代码\r\nint main() { return 1; }\r\n"}
	var original Ref
	for i, content := range contents {
		p.Files[0].CurrentContent = content
		ref, err := s.UpsertSnapshot(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			original = ref
		} else if ref != original {
			t.Fatal("changing current editor code changed the semantic or execution contract")
		}
		got, err := s.Snapshot(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Files) != 1 || got.Files[0].CurrentContent != content || got.Files[0].CurrentContentHash != problem.HashContent(content) {
			t.Fatalf("editor snapshot bytes or hash were lost: %+v", got.Files)
		}
		if p.Files[0].CurrentContent != content {
			t.Fatal("upsert mutated caller's editor code")
		}
		var raw string
		if err = s.db.QueryRowContext(ctx, "SELECT contract_json FROM execution_contracts WHERE contract_hash=?", ref.ContractHash).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var contract problem.Snapshot
		if err = json.Unmarshal([]byte(raw), &contract); err != nil {
			t.Fatal(err)
		}
		if len(contract.Files) != 1 || contract.Files[0].CurrentContent != "" || contract.Files[0].CurrentContentHash != "" {
			t.Fatal("execution contract retained changing editor code")
		}
	}
}

func TestStorePersistenceCandidatesPromotionAndFailures(t *testing.T) {
	dir := t.TempDir()
	dbpath := filepath.Join(dir, "bank.sqlite")
	ctx := context.Background()
	s, err := Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "answer"}}, Source: "model", Provider: "fake", Model: "flash", PromptVersion: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordValidation(ctx, Validation{CandidateID: c.ID, Kind: "selftest", Result: "failed", FilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Feedback: "wrong output", TestCount: 1, PassedCount: 0}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordValidation(ctx, Validation{CandidateID: c.ID, Kind: "formal", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != "upstream_verified" {
		t.Fatalf("unexpected promoted candidate: %+v", got)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.Lookup(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Validations) != 2 {
		t.Fatalf("history not persisted: %+v", got)
	}
}

func TestStoreRunIdempotencyUsageAndCanary(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "runs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	raw, _ := json.Marshal(map[string]any{"stage": "selftest", "input_tokens": 4})
	r := RunRecord{ID: "run-1", Owner: "owner-hash", IdempotencyKey: "same", Status: "queued", Data: raw, CreatedAt: now, UpdatedAt: now}
	if err = s.PutRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.FindRunByIdempotency(ctx, "owner-hash", "same")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r.ID {
		t.Fatal(got)
	}
	if err = s.RecordUsage(ctx, Usage{RunID: r.ID, Provider: "fake", Model: "flash", PricingVersion: "v1", InputTokens: 4, OutputTokens: 3, ReservedMicroUSD: 10, CostMicroUSD: 8, Status: "settled", FinishReason: "stop"}); err != nil {
		t.Fatal(err)
	}
	bad, _ := json.Marshal(map[string]any{"sec_key": "hidden"})
	r.ID = "run-2"
	r.IdempotencyKey = ""
	r.Data = bad
	if err = s.PutRun(ctx, r); err == nil {
		t.Fatal("private run data accepted")
	}
}
