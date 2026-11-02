package questionbank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func TestCollectionTargetsAndEvidenceHaveNoGlobalWindow(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := "platform:" + strings.Repeat("a", 32)
	other := "platform:" + strings.Repeat("b", 32)
	ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "verified fixture"}}, Source: "model"})
	if err != nil {
		t.Fatal(err)
	}
	target := CollectionTarget{Course: "Z_OLD", HomeworkID: 99, Game: "LEGACY", ChallengeID: 7, Shixun: "S"}
	// This is the original solver.Target JSON shape, including HomeworkID.
	raw, _ := json.Marshal(map[string]any{"target": map[string]any{"Course": target.Course, "HomeworkID": target.HomeworkID, "Game": target.Game}, "snapshot_ref": ref, "result": map[string]any{"candidate_id": candidate.ID, "code_hash": candidate.FilesHash}})
	now := time.Now().UTC()
	if err = s.PutRun(ctx, RunRecord{ID: "old_verified", Owner: problem.HashContent(owner), Status: "passed", Data: raw, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: "formal", Result: "passed", RunID: "old_verified", FilesHash: candidate.FilesHash, SavedFilesHash: candidate.FilesHash, SubmittedFilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 601; i++ {
		seed := CollectionTarget{Course: fmt.Sprintf("C%04d", i), HomeworkID: 1, Game: fmt.Sprintf("G%04d", i), ChallengeID: 7, Shixun: "S"}
		if err = s.RememberCollectionTarget(ctx, owner, seed); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"target": CollectionTarget{Course: "NEWER", HomeworkID: 42, Game: fmt.Sprintf("N%04d", i)}})
		if err = s.PutRun(ctx, RunRecord{ID: fmt.Sprintf("new_run_%04d", i), Owner: problem.HashContent(owner), Status: "passed", Data: body, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	seed := CollectionTarget{Course: "Z_REMEMBERED", HomeworkID: 88, Game: "REMEMBERED", ChallengeID: 7, Shixun: "S"}
	if err = s.RememberCollectionTarget(ctx, owner, seed); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []CollectionTarget{seed, target} {
		got, err := s.CollectionTargetsForHomework(ctx, owner, scope.Course, scope.HomeworkID)
		if err != nil || len(got) != 1 || got[0].Game != scope.Game || got[0].HomeworkID != scope.HomeworkID {
			t.Fatalf("old scoped evidence missing: %+v %v", got, err)
		}
		got, err = s.CollectionTargetsForHomework(ctx, other, scope.Course, scope.HomeworkID)
		if err != nil || len(got) != 0 {
			t.Fatalf("owner isolation: %+v %v", got, err)
		}
	}
	all, err := s.CollectionTargets(ctx, owner, 0)
	if err != nil || len(all) != 1204 {
		t.Fatalf("unlimited seeds=%d %v", len(all), err)
	}
	all, err = s.CollectionTargets(ctx, owner, 2000)
	if err != nil || len(all) != 1204 {
		t.Fatalf("large positive compatibility seeds=%d %v", len(all), err)
	}
	proven, err := s.HasCollectionCodeEvidence(ctx, owner, target, ref, candidate.FilesHash)
	if err != nil || !proven {
		t.Fatalf("formal exact evidence older than500 lost: %t %v", proven, err)
	}
	proven, err = s.HasCollectionCodeEvidence(ctx, other, target, ref, candidate.FilesHash)
	if err != nil || proven {
		t.Fatalf("formal evidence crossed owner: %t %v", proven, err)
	}
	target.Game = "OTHER"
	proven, err = s.HasCollectionCodeEvidence(ctx, owner, target, ref, candidate.FilesHash)
	if err != nil || proven {
		t.Fatalf("formal evidence crossed game: %t %v", proven, err)
	}
}
