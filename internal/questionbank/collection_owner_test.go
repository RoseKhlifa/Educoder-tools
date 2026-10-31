package questionbank

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func TestCollectionTargetsAndEvidenceSupportStablePlatformOwner(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "collection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owners := []string{"platform:" + strings.Repeat("a", 32), problem.HashContent("legacy-fixture")}
	target := CollectionTarget{Course: "COURSE_A", HomeworkID: 42, Game: "fixture_game", ChallengeID: 7, Shixun: "fixture_shixun"}
	ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "fixture candidate source"}}, Source: "model"})
	if err != nil {
		t.Fatal(err)
	}
	for i, owner := range owners {
		if err = s.RememberCollectionTarget(ctx, owner, target); err != nil {
			t.Fatalf("owner %s could not remember target: %v", owner, err)
		}
		got, e := s.CollectionTargets(ctx, owner, 10)
		if e != nil || len(got) != 1 || got[0] != target {
			t.Fatalf("owner %s cannot read own target: %+v %v", owner, got, e)
		}
		runID := []string{"platform-verified-run", "legacy-verified-run"}[i]
		body, _ := json.Marshal(map[string]any{"target": target, "snapshot_ref": ref, "result": map[string]any{"candidate_id": candidate.ID, "code_hash": candidate.FilesHash}})
		now := time.Now().UTC()
		if err = s.PutRun(ctx, RunRecord{ID: runID, Owner: problem.HashContent(owner), Status: "passed", Data: body, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err = s.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: "formal", Result: "passed", RunID: runID, FilesHash: candidate.FilesHash, SavedFilesHash: candidate.FilesHash, SubmittedFilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}); err != nil {
			t.Fatal(err)
		}
		confirmed, e := s.HasCollectionCodeEvidence(ctx, owner, target, ref, candidate.FilesHash)
		if e != nil || !confirmed {
			t.Fatalf("owner %s lost its once-hashed formal evidence: %v %v", owner, confirmed, e)
		}
		if confirmed, e = s.HasCollectionCodeEvidence(ctx, "platform:"+strings.Repeat("b", 32), target, ref, candidate.FilesHash); e != nil || confirmed {
			t.Fatalf("formal evidence crossed owner boundary: %v %v", confirmed, e)
		}
		if confirmed, e = s.HasCollectionCodeEvidence(ctx, problem.HashContent(owner), target, ref, candidate.FilesHash); e != nil || confirmed {
			t.Fatalf("double-hashed owner accidentally claimed raw-owner evidence: %v %v", confirmed, e)
		}
	}
	other, e := s.CollectionTargets(ctx, "platform:"+strings.Repeat("b", 32), 10)
	if e != nil || len(other) != 0 {
		t.Fatal("remembered target crossed tenant boundary")
	}
}

func TestCollectionOwnerRejectsUnrecognizedOrMalformedNamespaces(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "collection.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	target := CollectionTarget{Course: "COURSE_A", HomeworkID: 42, Game: "fixture_game", ChallengeID: 7, Shixun: "fixture_shixun"}
	for _, owner := range []string{"", "student", "tenant:" + strings.Repeat("a", 32), "platform:" + strings.Repeat("a", 31), "platform:" + strings.Repeat("a", 33), "platform:" + strings.Repeat("A", 32), "platform:" + strings.Repeat("g", 32), strings.Repeat("a", 64) + "suffix"} {
		if err = s.RememberCollectionTarget(ctx, owner, target); err == nil {
			t.Errorf("target accepted invalid owner %q", owner)
		}
		if _, err = s.CollectionTargets(ctx, owner, 10); err == nil {
			t.Errorf("target query accepted invalid owner %q", owner)
		}
		if _, err = s.HasCollectionCodeEvidence(ctx, owner, target, Ref{}, strings.Repeat("a", 64)); err == nil {
			t.Errorf("evidence accepted invalid owner %q", owner)
		}
	}
}
