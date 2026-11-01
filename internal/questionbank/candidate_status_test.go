package questionbank

import (
	"context"
	"testing"

	"educoder-cli/internal/problem"
)

func TestAnyFailedSampleKeepsCandidateRejectedUntilFormalPass(t *testing.T) {
	for _, order := range [][]string{{"failed", "passed"}, {"passed", "failed"}} {
		t.Run(order[0]+"-then-"+order[1], func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ref, err := db.UpsertSnapshot(ctx, bankSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := db.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "synthetic"}}, Source: "model"})
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range order {
				passed := 0
				if result == "passed" {
					passed = 1
				}
				err := db.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: "selftest", Result: result, FilesHash: candidate.FilesHash, SavedFilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: passed})
				if err != nil {
					t.Fatal(err)
				}
			}
			all, err := db.Lookup(ctx, ref)
			if err != nil || len(all) != 1 || all[0].Status != "rejected" || len(all[0].Validations) != 2 {
				t.Fatalf("later successful sample erased known failure: %+v err=%v", all, err)
			}
			if err := db.RecordValidation(ctx, Validation{CandidateID: candidate.ID, Kind: "formal", Result: "passed", FilesHash: candidate.FilesHash, SavedFilesHash: candidate.FilesHash, SubmittedFilesHash: candidate.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}); err != nil {
				t.Fatal(err)
			}
			all, err = db.Lookup(ctx, ref)
			if err != nil || len(all) != 1 || all[0].Status != "upstream_verified" {
				t.Fatalf("complete formal proof should remain eligible: %+v err=%v", all, err)
			}
		})
	}
}
