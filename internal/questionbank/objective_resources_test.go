package questionbank

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"testing"

	"educoder-cli/internal/answer"
)

func objectiveBoundImage(t *testing.T, value byte, position int, option string) answer.ImageResource {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.SetRGBA(0, 0, color.RGBA{R: value, A: 255})
	var content bytes.Buffer
	if err := png.Encode(&content, im); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content.Bytes())
	return answer.ImageResource{QuestionPosition: position, OptionLabel: option, MediaType: "image/png", Content: content.Bytes(), SHA256: hex.EncodeToString(digest[:])}
}

func objectiveBoundSnapshot(t *testing.T) (answer.ObjectiveSnapshot, []answer.ImageResource) {
	t.Helper()
	snapshot := objectiveCompatSnapshot()
	resources := []answer.ImageResource{objectiveBoundImage(t, 32, 7, ""), objectiveBoundImage(t, 64, 7, "B")}
	snapshot.Questions[0].Question += ` <img src="sha256:` + resources[0].SHA256 + `">`
	snapshot.Questions[0].Options[1].Text += ` <img src="sha256:` + resources[1].SHA256 + `">`
	return snapshot, resources
}

func objectiveBoundStore(t *testing.T, snapshot answer.ObjectiveSnapshot) (*Store, string, ObjectiveCandidate) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	key, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]string, len(snapshot.Questions))
	for i := range values {
		values[i] = "A"
	}
	candidate, err := store.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: values}, "bound-image-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordObjectiveVerification(ctx, candidate.ID, "fixture-full-score", "full_score", "13.30", "13.3"); err != nil {
		t.Fatal(err)
	}
	return store, key, candidate
}

func TestObjectiveBoundImageLookupReusesFullScoreDespiteLaterPartial(t *testing.T) {
	ctx := context.Background()
	snapshot, resources := objectiveBoundSnapshot(t)
	store, key, fullScore := objectiveBoundStore(t, snapshot)
	partial, err := store.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}, "later-partial")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordObjectiveVerification(ctx, partial.ID, "fixture-partial", "not_full_score", "9.31", "13.3"); err != nil {
		t.Fatal(err)
	}
	for _, dataURL := range []bool{false, true} {
		current := append([]answer.ImageResource(nil), resources...)
		if dataURL {
			for i := range current {
				current[i].DataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(current[i].Content)
				current[i].Content = nil
			}
		}
		got, err := store.FindObjectiveVerifiedWithResources(ctx, key, current)
		if err != nil || got.ID != fullScore.ID || !reflect.DeepEqual(got.Candidate, fullScore.Candidate) {
			t.Fatalf("exact hash-bound full-score candidate not reused: id=%d want=%d err=%v", got.ID, fullScore.ID, err)
		}
	}
	if _, err = store.FindObjectiveVerified(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy caller reused images without current resources: %v", err)
	}
}

func TestObjectiveBoundImageLookupRejectsMissingChangedOrInvalidResources(t *testing.T) {
	ctx := context.Background()
	snapshot, resources := objectiveBoundSnapshot(t)
	store, key, _ := objectiveBoundStore(t, snapshot)
	for _, test := range []struct {
		name   string
		mutate func([]answer.ImageResource) []answer.ImageResource
	}{
		{"all-missing", func(r []answer.ImageResource) []answer.ImageResource { return nil }},
		{"one-missing", func(r []answer.ImageResource) []answer.ImageResource { return r[:1] }},
		{"hash-missing", func(r []answer.ImageResource) []answer.ImageResource { r[0].SHA256 = ""; return r }},
		{"hash-tampered", func(r []answer.ImageResource) []answer.ImageResource { r[0].SHA256 = r[1].SHA256; return r }},
		{"changed-valid-image", func(r []answer.ImageResource) []answer.ImageResource {
			r[0] = objectiveBoundImage(t, 128, 7, "")
			return r
		}},
		{"position-wrong", func(r []answer.ImageResource) []answer.ImageResource { r[0].QuestionPosition = 99; return r }},
		{"option-wrong", func(r []answer.ImageResource) []answer.ImageResource { r[1].OptionLabel = "A"; return r }},
		{"bytes-missing", func(r []answer.ImageResource) []answer.ImageResource { r[0].Content = nil; return r }},
		{"mime-wrong", func(r []answer.ImageResource) []answer.ImageResource { r[0].MediaType = "image/jpeg"; return r }},
		{"invalid-image", func(r []answer.ImageResource) []answer.ImageResource {
			r[0].Content = []byte("not an image")
			digest := sha256.Sum256(r[0].Content)
			r[0].SHA256 = hex.EncodeToString(digest[:])
			return r
		}},
		{"invalid-dimensions", func(r []answer.ImageResource) []answer.ImageResource {
			r[0].Content = append([]byte(nil), r[0].Content...)
			binary.BigEndian.PutUint32(r[0].Content[16:20], 100001)
			binary.BigEndian.PutUint32(r[0].Content[20:24], 100001)
			binary.BigEndian.PutUint32(r[0].Content[29:33], crc32.ChecksumIEEE(r[0].Content[12:29]))
			digest := sha256.Sum256(r[0].Content)
			r[0].SHA256 = hex.EncodeToString(digest[:])
			return r
		}},
		{"unreferenced-resource", func(r []answer.ImageResource) []answer.ImageResource {
			return append(r, objectiveBoundImage(t, 255, 7, "A"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := test.mutate(append([]answer.ImageResource(nil), resources...))
			if _, err := store.FindObjectiveVerifiedWithResources(ctx, key, current); err == nil {
				t.Fatal("invalid or mismatched resource produced a cache hit")
			}
		})
	}
}

func TestObjectiveBoundImageLookupRejectsRemoteOrUnsupportedMedia(t *testing.T) {
	ctx := context.Background()
	for _, markup := range []string{
		`<img src="https://example.org/tree.png">`,
		`![tree](https://example.org/tree.png)`,
		`<img src="sha256:not-a-digest">`,
		`<img src="sha256:0000000000000000000000000000000000000000000000000000000000000000" alt="unsupported">`,
		`<svg><path/></svg>`,
		`<picture><source srcset="https://example.org/tree.png"></picture>`,
	} {
		t.Run(markup, func(t *testing.T) {
			snapshot := objectiveCompatSnapshot()
			snapshot.Questions[0].Question += " " + markup
			store, key, _ := objectiveBoundStore(t, snapshot)
			for _, resources := range [][]answer.ImageResource{nil, {objectiveBoundImage(t, 32, 7, "")}} {
				if _, err := store.FindObjectiveVerifiedWithResources(ctx, key, resources); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unbound or unsupported media became reusable: %v", err)
				}
			}
			if _, err := store.FindObjectiveVerified(ctx, key); !errors.Is(err, ErrNotFound) {
				t.Fatalf("legacy lookup accepted unsupported media: %v", err)
			}
		})
	}
}

func TestObjectiveBoundImageLookupRequiresSharedImageForEveryQuestion(t *testing.T) {
	ctx := context.Background()
	snapshot := objectiveCompatSnapshot()
	second := objectiveTestQuestion()
	second.Position = 19
	second.Question = "Which traversal visits the root last?"
	snapshot.Questions = append(snapshot.Questions, second)
	resource := objectiveBoundImage(t, 32, 7, "")
	snapshot.Statement = `Compare the tree: <img src="sha256:` + resource.SHA256 + `">`
	store, key, candidate := objectiveBoundStore(t, snapshot)
	if _, err := store.FindObjectiveVerifiedWithResources(ctx, key, []answer.ImageResource{resource}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("shared image was not required for the second question: %v", err)
	}
	secondResource := resource
	secondResource.QuestionPosition = second.Position
	got, err := store.FindObjectiveVerifiedWithResources(ctx, key, []answer.ImageResource{secondResource, resource})
	if err != nil || got.ID != candidate.ID {
		t.Fatalf("shared image with complete position bindings was not reused: %v", err)
	}
}

func TestObjectiveBoundImageLookupKeepsUnverifiedWholeSnapshotAMiss(t *testing.T) {
	ctx := context.Background()
	snapshot, resources := objectiveBoundSnapshot(t)
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, "fixture-partial")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordObjectiveVerification(ctx, candidate.ID, "partial", "not_full_score", "9.31", "13.3"); err != nil {
		t.Fatal(err)
	}
	questionResources := make([]ObjectiveResource, len(resources))
	for i, resource := range resources {
		questionResources[i] = ObjectiveResource{Locator: "sha256:" + resource.SHA256, SHA256: resource.SHA256}
	}
	questionKey, err := store.UpsertObjectiveQuestionWithResources(ctx, snapshot.Questions[0], questionResources)
	if err != nil {
		t.Fatal(err)
	}
	answerID, err := store.AddObjectiveAnswer(ctx, questionKey, "A", "fixture-verified-single", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordObjectiveValidation(ctx, answerID, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.FindObjectiveVerifiedWithResources(ctx, key, resources); !errors.Is(err, ErrNotFound) {
		t.Fatalf("valid image bytes promoted an unverified candidate: %v", err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM objective_verifications WHERE result='full_score'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("image lookup fabricated whole-snapshot verification: count=%d err=%v", count, err)
	}
}

func TestObjectiveVerifiedLookupRejectsCandidateAfterLaterPartialVerification(t *testing.T) {
	ctx := context.Background()
	snapshot, resources := objectiveBoundSnapshot(t)
	store, key, candidate := objectiveBoundStore(t, snapshot)
	if err := store.RecordObjectiveVerification(ctx, candidate.ID, "fixture-later-partial", "not_full_score", "10.71", "14.28"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindObjectiveVerifiedWithResources(ctx, key, resources); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale full-score candidate was reused after current partial verification: %v", err)
	}
}
