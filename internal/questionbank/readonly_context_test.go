package questionbank

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"educoder-cli/internal/problem"
)

func TestReadOnlyContextUpdatesSnapshotButPreservesLegacyCandidateIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "context.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := bankSnapshot()
	ref, err := s.UpsertSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: p.Files[0].Path, Content: "answer"}}, Source: "model"})
	if err != nil {
		t.Fatal(err)
	}
	p.ReadOnlyFiles = []problem.ReadOnlyFile{{Path: "src/driver.cpp", Language: "cpp", Content: "int main(){return 0;}"}}
	p.ReadOnlyContext = &problem.ReadOnlyContextDiagnostic{Status: "available", Stage: "association", Reason: "observed_driver"}
	next, err := s.UpsertSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if ref != next {
		t.Fatal("read-only observation invalidated legacy bank identity")
	}
	found, err := s.Lookup(ctx, next)
	if err != nil || len(found) != 1 || found[0].ID != candidate.ID {
		t.Fatalf("legacy candidate lost: %+v %v", found, err)
	}
	stored, err := s.Snapshot(ctx, next)
	if err != nil || len(stored.ReadOnlyFiles) != 1 || stored.ContextHash == "" || stored.ReadOnlyContext == nil || stored.ReadOnlyContext.Reason != "observed_driver" {
		t.Fatalf("latest snapshot context missing: %+v %v", stored, err)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT contract_json FROM execution_contracts WHERE contract_hash=?", next.ContractHash).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var contract problem.Snapshot
	if json.Unmarshal([]byte(raw), &contract) != nil || len(contract.ReadOnlyFiles) != 0 || contract.ContextHash != "" || contract.ReadOnlyContext != nil {
		t.Fatalf("stable contract retained caller observation: %s", raw)
	}
	// A fresh contract first encountered with a driver must also exclude it.
	p.Files[0].Path = "fresh/answer.h"
	p.Files[0].Filename = ""
	fresh, err := s.UpsertSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT contract_json FROM execution_contracts WHERE contract_hash=?", fresh.ContractHash).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(raw), &contract) != nil || len(contract.ReadOnlyFiles) != 0 || contract.ContextHash != "" || contract.ReadOnlyContext != nil {
		t.Fatalf("new stable contract retained caller observation: %s", raw)
	}
}
