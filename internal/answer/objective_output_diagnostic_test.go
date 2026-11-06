package answer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestObjectiveOutputDiagnosticIncludesOnlyStructuralMetadata(t *testing.T) {
	err := &Error{Kind: "objective_candidate_contract", ObjectiveOutput: &ObjectiveOutputViolation{
		Reason: ObjectiveOutputAnswerCount, ExpectedAnswers: 1, ReceivedAnswers: 7,
		QuestionPosition: 12, Shape: ObjectiveShapeSingle, OptionCount: 4,
	}}
	want := "model provider objective_candidate_contract [objective_output reason=answer_count expected_answers=1 received_answers=7 position=12 shape=single option_count=4]"
	if got := err.Error(); got != want {
		t.Fatalf("structural error=%q want=%q", got, want)
	}
	feedback, ok := ObjectiveOutputFeedback(fmt.Errorf("irrelevant wrapper: %w", err))
	if !ok || feedback.Kind != "objective_output_contract" || feedback.Stdin != "" || feedback.ExpectedStdout != "" || feedback.ActualStdout != "" {
		t.Fatalf("unsafe or missing feedback: %+v ok=%v", feedback, ok)
	}
	for _, want := range []string{"exactly 1 entries", "rejected output had 7 entries", "original position 12", "context_questions are reference context only", "single choice: return exactly one", "contains 4 allowed options"} {
		if !strings.Contains(feedback.Message, want) {
			t.Errorf("missing correction %q in %q", want, feedback.Message)
		}
	}
	if strings.Contains(feedback.Message, "irrelevant wrapper") {
		t.Fatal("feedback copied underlying error text")
	}
}

func TestObjectiveOutputFeedbackClassifiesOnlyKnownContractViolations(t *testing.T) {
	for _, reason := range []ObjectiveOutputReason{ObjectiveOutputKind, ObjectiveOutputAnswerCount, ObjectiveOutputAnswerShape, ObjectiveOutputUnknownOption, ObjectiveOutputDuplicateOption, ObjectiveOutputOptionOrder} {
		t.Run(string(reason), func(t *testing.T) {
			err := &Error{Kind: "objective_candidate_contract", ObjectiveOutput: &ObjectiveOutputViolation{Reason: reason, ExpectedAnswers: 2, ReceivedAnswers: 2, QuestionPosition: 3, Shape: ObjectiveShapeMultiple, OptionCount: 4}}
			feedback, ok := ObjectiveOutputFeedback(err)
			if !ok || !strings.Contains(feedback.Message, "multiple choice: return one or more distinct") || !strings.Contains(err.Error(), "reason="+string(reason)) {
				t.Fatalf("known structure reason rejected: %+v ok=%v err=%v", feedback, ok, err)
			}
		})
	}
	var nilProviderErr *Error
	for _, err := range []error{
		nil, nilProviderErr, errors.New("raw-private-answer-sentinel"),
		&Error{Kind: "objective_candidate_contract"},
		&Error{Kind: "invalid_output_json", ObjectiveOutput: &ObjectiveOutputViolation{Reason: ObjectiveOutputAnswerCount}},
		&Error{Kind: "objective_fixed_answers_mismatch", ObjectiveOutput: &ObjectiveOutputViolation{Reason: ObjectiveOutputAnswerShape}},
		&Error{Kind: "objective_candidate_contract", ObjectiveOutput: &ObjectiveOutputViolation{Reason: "raw-private-answer-sentinel"}},
	} {
		if feedback, ok := ObjectiveOutputFeedback(err); ok || feedback != (Feedback{}) {
			t.Fatalf("non-structural failure generated correction: %+v ok=%v", feedback, ok)
		}
	}
}

func TestObjectiveOutputDiagnosticSanitizesEnumsAndNegativeMetadata(t *testing.T) {
	const private = "raw-private-answer-sentinel"
	err := &Error{Kind: "objective_candidate_contract", ObjectiveOutput: &ObjectiveOutputViolation{
		Reason: ObjectiveOutputAnswerShape, ExpectedAnswers: -1, ReceivedAnswers: -2,
		QuestionPosition: -3, Shape: ObjectiveAnswerShape(private), OptionCount: -4,
	}}
	got := err.Error()
	if strings.Contains(got, private) || strings.Contains(got, "=-") || !strings.Contains(got, "shape=unspecified") {
		t.Fatalf("invalid structure metadata leaked: %q", got)
	}
	feedback, ok := ObjectiveOutputFeedback(err)
	if !ok || strings.Contains(feedback.Message, private) || strings.Contains(feedback.Message, "exactly -") || !strings.Contains(feedback.Message, "For single choice use exactly one") {
		t.Fatalf("unsafe fallback correction: %+v ok=%v", feedback, ok)
	}
	// Formatting must not mutate the original evidence or trust an unknown
	// reason supplied by an unrelated caller.
	if err.ObjectiveOutput.Shape != ObjectiveAnswerShape(private) || err.ObjectiveOutput.ExpectedAnswers != -1 {
		t.Fatal("diagnostic rendering mutated error metadata")
	}
	err.ObjectiveOutput.Reason = ObjectiveOutputReason(private)
	if got := err.Error(); strings.Contains(got, private) || strings.Contains(got, "objective_output") {
		t.Fatalf("unknown reason leaked into error: %q", got)
	}
}
