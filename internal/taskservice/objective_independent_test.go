package taskservice

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"educoder-cli/internal/answer"
)

func independentObjectiveFixture() (answer.ObjectiveSnapshot, *answer.ObjectiveCandidate) {
	snapshot := answer.ObjectiveSnapshot{Kind: "objective", Title: "Shared title", Statement: "Complete shared context", Questions: []answer.ObjectiveQuestion{
		{Position: 3, Question: "First question", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "First", Position: 0}, {Label: "B", Text: "Second", Position: 1}}},
		{Position: 7, Question: "Second question", Category: 2, Options: []answer.ObjectiveOption{{Label: "A", Text: "Alpha", Position: 0}, {Label: "B", Text: "Beta", Position: 1}}},
		{Position: 11, Question: "Third question", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "Yes", Position: 0}, {Label: "B", Text: "No", Position: 1}}},
	}}
	return snapshot, &answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "AB", "A"}}
}

func TestObjectiveIndependentReviewAssemblesOnlyCompleteVectorAndPreservesLocks(t *testing.T) {
	snapshot, previous := independentObjectiveFixture()
	s, err := newObjectiveIndependentReview(snapshot, nil, previous, []string{"B", "", ""})
	if err != nil || s.remaining != 2 {
		t.Fatalf("review initialization failed: %+v %v", s, err)
	}
	for step, position := range []int{7, 11} {
		request, ok := s.nextRequest(answer.RepairProfile())
		if !ok || request.Previous != nil || len(request.Feedback) != 0 || request.FixedAnswers != nil || len(request.Problem.Questions) != 1 || request.Problem.Questions[0].Position != position || request.Problem.Title != snapshot.Title || request.Problem.Statement != snapshot.Statement || request.Profile != answer.RepairProfile() || request.Context == nil || !reflect.DeepEqual(*request.Context, snapshot) {
			t.Fatalf("request %d lost identity/context or retained answer anchoring: %+v", step, request)
		}
		full, done, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{" b "}})
		if err != nil || done != (step == 1) || s.remaining != 1-step {
			t.Fatalf("accept %d failed: %+v done=%v remaining=%d err=%v", step, full, done, s.remaining, err)
		}
		if !done && (full.Kind != "" || full.Answers != nil) {
			t.Fatalf("partial mixed vector escaped: %+v", full)
		}
		if done && !reflect.DeepEqual(full, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B", "B", "B"}}) {
			t.Fatalf("review changed a lock or merged into wrong index: %+v", full)
		}
	}
	if _, ok := s.nextRequest(answer.DefaultProfile()); ok {
		t.Fatal("completed sweep issued another request")
	}
	if _, _, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}); err == nil {
		t.Fatal("completed sweep accepted an extra answer")
	}
}

func TestObjectiveIndependentReviewRejectsIncompleteInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*answer.ObjectiveSnapshot, **answer.ObjectiveCandidate, *[]string)
	}{
		{"missing previous", func(_ *answer.ObjectiveSnapshot, p **answer.ObjectiveCandidate, _ *[]string) { *p = nil }},
		{"missing question", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) { s.Questions = nil }},
		{"blank question", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[1].Question = "  "
		}},
		{"missing option", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[0].Options = s.Questions[0].Options[:1]
		}},
		{"blank option", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[0].Options[1].Text = ""
		}},
		{"duplicate question position", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[1].Position = s.Questions[0].Position
		}},
		{"duplicate option position", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[0].Options[1].Position = 0
		}},
		{"unknown category", func(s *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, _ *[]string) {
			s.Questions[0].Category = 0
		}},
		{"partial previous", func(_ *answer.ObjectiveSnapshot, p **answer.ObjectiveCandidate, _ *[]string) {
			(*p).Answers = []string{"A"}
		}},
		{"partial locks", func(_ *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, f *[]string) { *f = []string{"A"} }},
		{"invalid locked option", func(_ *answer.ObjectiveSnapshot, _ **answer.ObjectiveCandidate, f *[]string) {
			*f = []string{"Z", "", ""}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, previous := independentObjectiveFixture()
			var fixed []string
			tc.mutate(&snapshot, &previous, &fixed)
			if state, err := newObjectiveIndependentReview(snapshot, nil, previous, fixed); err == nil || state != nil {
				t.Fatalf("invalid input started review: %+v err=%v", state, err)
			}
		})
	}
}

func TestObjectiveIndependentReviewInvalidResponseDoesNotAdvance(t *testing.T) {
	snapshot, previous := independentObjectiveFixture()
	s, err := newObjectiveIndependentReview(snapshot, nil, previous, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}); err == nil || s.remaining != 3 {
		t.Fatal("accepted answer without issuing its request")
	}
	for _, invalid := range []answer.ObjectiveCandidate{
		{Kind: "programming", Answers: []string{"B"}},
		{Kind: "objective", Answers: []string{"B", "A"}},
		{Kind: "objective", Answers: []string{"Z"}},
		{Kind: "objective", Answers: []string{"AB"}},
	} {
		request, _ := s.nextRequest(answer.DefaultProfile())
		full, done, err := s.accept(invalid)
		if err == nil || done || full.Answers != nil || s.remaining != 3 || request.Problem.Questions[0].Position != 3 {
			t.Fatalf("invalid answer advanced sweep: full=%+v done=%v remaining=%d err=%v", full, done, s.remaining, err)
		}
	}
	if _, done, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}); err != nil || done || s.remaining != 2 {
		t.Fatalf("valid answer after failures did not advance: %v remaining=%d", err, s.remaining)
	}
}

func TestObjectiveIndependentReviewPreservesSiblingContextAndOwnsAllSlices(t *testing.T) {
	snapshot, previous := independentObjectiveFixture()
	snapshot.Questions[1].Question = "Use the first question's figure and third question's option A"
	image := objectiveResourcePNG(t, 1)
	resources := []answer.ImageResource{
		{MediaType: "image/png", Content: image, Bindings: []answer.ImageBinding{{QuestionPosition: 3}, {QuestionPosition: 7}, {QuestionPosition: 7, OptionLabel: "B"}}},
		{QuestionPosition: 11, OptionLabel: "A", MediaType: "image/png", Content: append([]byte(nil), image...)},
	}
	fixed := []string{"A", "", "B"}
	s, err := newObjectiveIndependentReview(snapshot, resources, previous, fixed)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Title = "mutated"
	snapshot.Statement = "mutated"
	snapshot.Questions[1].Options[0].Text = "mutated"
	previous.Answers[0] = "B"
	fixed[0] = "B"
	resources[0].Content[0] = 0
	resources[0].Bindings[1].QuestionPosition = 99
	request, ok := s.nextRequest(answer.DefaultProfile())
	if !ok || len(request.Resources) != 2 || request.Problem.Title != "Shared title" || request.Problem.Statement != "Complete shared context" || request.Problem.Questions[0].Options[0].Text != "Alpha" || request.Context == nil || len(request.Context.Questions) != 3 || request.Context.Title != "Shared title" || request.Context.Statement != "Complete shared context" || request.Context.Questions[1].Options[0].Text != "Alpha" {
		t.Fatalf("source aliases changed review: %+v", request)
	}
	wantBindings := []answer.ImageBinding{{QuestionPosition: 3}, {QuestionPosition: 7}, {QuestionPosition: 7, OptionLabel: "B"}}
	if !reflect.DeepEqual(request.Resources[0].EffectiveBindings(), wantBindings) || request.Resources[0].QuestionPosition != 3 || request.Resources[0].Content[0] != 137 || !reflect.DeepEqual(request.Resources[1].EffectiveBindings(), []answer.ImageBinding{{QuestionPosition: 11, OptionLabel: "A"}}) {
		t.Fatalf("shared image mapping/content lost: %+v", request.Resources[0].EffectiveBindings())
	}
	if err := answer.ValidateObjectiveResources(*request.Context, request.Resources); err != nil {
		t.Fatal(err)
	}
	request.Problem.Questions[0].Options[0].Text = "caller mutation"
	if request.Context.Questions[1].Options[0].Text != "Alpha" {
		t.Fatal("selected target aliases its context roster")
	}
	request.Context.Questions[0].Options[0].Text = "context caller mutation"
	request.Context.Questions[1].Options[0].Text = "context target mutation"
	request.Resources[0].Content[0] = 1
	request.Resources[0].Bindings[0].QuestionPosition = 100
	request.Resources[1].Content[0] = 2
	request.Resources[1].Bindings[0].OptionLabel = "B"
	again, _ := s.nextRequest(answer.DefaultProfile())
	if again.Problem.Questions[0].Options[0].Text != "Alpha" || again.Context.Questions[0].Options[0].Text != "First" || again.Context.Questions[1].Options[0].Text != "Alpha" || again.Resources[0].Content[0] != 137 || again.Resources[1].Content[0] != 137 || !reflect.DeepEqual(again.Resources[0].EffectiveBindings(), wantBindings) || !reflect.DeepEqual(again.Resources[1].EffectiveBindings(), []answer.ImageBinding{{QuestionPosition: 11, OptionLabel: "A"}}) {
		t.Fatal("request aliases changed future retry context")
	}
	full, done, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}})
	if err != nil || !done || !reflect.DeepEqual(full.Answers, []string{"A", "B", "B"}) {
		t.Fatalf("mutations changed locked answers: %+v done=%v err=%v", full, done, err)
	}
	full.Answers[0] = "B"
	if s.answers[0] != "A" {
		t.Fatal("returned vector aliases state")
	}
	encoded, _ := json.Marshal(again)
	if bytes.Contains(encoded, []byte("previous_candidate")) || bytes.Contains(encoded, []byte("formal_history")) {
		t.Fatal("independent review retained scored candidate history")
	}
}

func TestObjectiveIndependentReviewAllLockedSkipsSweep(t *testing.T) {
	snapshot, previous := independentObjectiveFixture()
	s, err := newObjectiveIndependentReview(snapshot, nil, previous, []string{"A", "AB", "B"})
	if err != nil || s.remaining != 0 {
		t.Fatalf("all locked review failed: %+v err=%v", s, err)
	}
	if _, ok := s.nextRequest(answer.DefaultProfile()); ok {
		t.Fatal("all locked vector consumed a model request")
	}
}

func TestObjectiveIndependentReviewPreflightRequestsDoNotIssueOrAdvance(t *testing.T) {
	snapshot, previous := independentObjectiveFixture()
	resources := []answer.ImageResource{{MediaType: "image/png", Content: objectiveResourcePNG(t, 1), Bindings: []answer.ImageBinding{{QuestionPosition: 3}, {QuestionPosition: 7}}}}
	s, err := newObjectiveIndependentReview(snapshot, resources, previous, []string{"", "", "B"})
	if err != nil {
		t.Fatal(err)
	}
	requests := s.requests(answer.RepairProfile())
	if len(requests) != 2 || s.remaining != 2 || s.index != 0 || s.issued || requests[0].Problem.Questions[0].Position != 3 || requests[1].Problem.Questions[0].Position != 7 {
		t.Fatalf("preflight advanced or changed queue: requests=%+v state=%+v", requests, s)
	}
	if _, _, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}); err == nil {
		t.Fatal("preflight authorized accepting an unissued answer")
	}
	requests[0].Context.Questions[1].Options[0].Text = "changed"
	requests[0].Resources[0].Content[0] = 0
	requests[0].Resources[0].Bindings[0].QuestionPosition = 99
	if requests[1].Context.Questions[1].Options[0].Text != "Alpha" || requests[1].Resources[0].Content[0] != 137 || requests[1].Resources[0].Bindings[0].QuestionPosition != 3 {
		t.Fatal("preflight requests share mutable context/media")
	}
	next, ok := s.nextRequest(answer.RepairProfile())
	if !ok || next.Context.Questions[1].Options[0].Text != "Alpha" || next.Resources[0].Content[0] != 137 || next.Resources[0].Bindings[0].QuestionPosition != 3 {
		t.Fatal("preflight mutation changed issued request")
	}
	before := s.requests(answer.RepairProfile())
	if len(before) != 2 || !s.issued || s.remaining != 2 {
		t.Fatal("preflight changed an already-issued review")
	}
	if _, done, err := s.accept(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}); err != nil || done {
		t.Fatalf("first review failed after preflight: done=%v err=%v", done, err)
	}
	remaining := s.requests(answer.DefaultProfile())
	if len(remaining) != 1 || remaining[0].Problem.Questions[0].Position != 7 || s.remaining != 1 || s.index != 1 || s.issued {
		t.Fatal("preflight did not preserve remaining queue")
	}
	if (*objectiveIndependentReview)(nil).requests(answer.DefaultProfile()) != nil {
		t.Fatal("nil review has preflight requests")
	}
}
