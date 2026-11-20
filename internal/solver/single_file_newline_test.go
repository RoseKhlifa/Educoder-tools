package solver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

const newlineCandidateLF = "int main() {\n  return 0;\n}\n"
const newlineCandidateCRLF = "int main() {\r\n  return 0;\r\n}\r\n"

type newlineCheckingAdapter struct {
	fakeAdapter
	store                       *fakeStore
	files                       []problem.SolutionFile
	formalFiles                 []problem.SolutionFile
	parentWasIntact             bool
	checkParent                 bool
	childWasInitiallyUnverified bool
}

func (a *newlineCheckingAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, _, expected string) (SelfTest, error) {
	a.selfTests++
	a.files = append(a.files, file)
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	if a.checkParent {
		a.parentWasIntact = a.store.candidates[0].Files[0].Content == newlineCandidateCRLF && a.store.candidates[0].Status == "upstream_verified"
	}
	for _, stored := range a.store.candidates {
		if stored.Source == "editor_newline_normalized" {
			a.childWasInitiallyUnverified = stored.Status == "candidate" && len(stored.Validations) == 0
		}
	}
	output := expected
	if file.Content != newlineCandidateLF {
		output = "wrong"
	}
	return SelfTest{Completed: true, Output: output, Matches: output == expected, SavedFilesHash: hash}, nil
}

func (a *newlineCheckingAdapter) Submit(_ context.Context, _ Target, file problem.SolutionFile) (Evaluation, error) {
	a.submits++
	a.formalFiles = append(a.formalFiles, file)
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	passed := file.Content == newlineCandidateLF
	return Evaluation{Completed: true, Passed: passed, Status: "2", TestCount: 1, PassedCount: boolInt(passed), SavedFilesHash: hash, SubmittedFilesHash: hash}, nil
}

func newlineBankSnapshot(mode string) problem.Snapshot {
	s := testSnapshot()
	s.EditMode = mode
	s.Files[0].CurrentContent = "starter"
	s.Files[0].Path = "main.cpp"
	s.Files[0].Filename = "main.cpp"
	s.Files[0].Language = "cpp"
	return s
}

func verifiedNewlineBankParent(t *testing.T) questionbank.Candidate {
	t.Helper()
	files := []problem.SolutionFile{{Path: "main.cpp", Content: newlineCandidateCRLF}}
	hash, err := problem.HashFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	return questionbank.Candidate{
		ID:             2580,
		Ref:            questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"},
		CandidateInput: questionbank.CandidateInput{Files: files, Source: "external", Provider: "provider-x", Model: "model-y", PromptVersion: "bank-v1"},
		FilesHash:      hash,
		Status:         "upstream_verified",
		Validations:    []questionbank.Validation{{CandidateID: 2580, Kind: "formal", Result: "passed", FilesHash: hash, SavedFilesHash: hash, SubmittedFilesHash: hash, RevisionHash: "rev", ContractHash: "contract", Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}},
	}
}

func TestSingleFileBankCRLFVerifiedParentCreatesLFChildAndReusesIt(t *testing.T) {
	parent := verifiedNewlineBankParent(t)
	store := &fakeStore{candidates: []questionbank.Candidate{parent}}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("must not be used")}}
	for run := 0; run < 2; run++ {
		adapter := &newlineCheckingAdapter{fakeAdapter: fakeAdapter{snap: newlineBankSnapshot("single_file")}, store: store, checkParent: true}
		req := Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit, IdempotencyKey: "newline-run-" + string(rune('1'+run))}
		result, err := New(store, provider, Config{Timeout: time.Second}).Run(context.Background(), adapter, req)
		if err != nil || result.Status != "passed" || result.ModelCalls != 0 || adapter.selfTests != 1 || adapter.submits != 1 {
			t.Fatalf("run %d result=%+v err=%v modelCalls=%d selftests=%d submits=%d", run, result, err, provider.calls, adapter.selfTests, adapter.submits)
		}
		if len(adapter.files) != 1 || adapter.files[0].Content != newlineCandidateLF || len(adapter.formalFiles) != 1 || adapter.formalFiles[0].Content != newlineCandidateLF {
			t.Fatalf("run %d did not test and submit exact LF source: self=%q formal=%q", run, adapter.files, adapter.formalFiles)
		}
		if !adapter.parentWasIntact {
			t.Fatalf("run %d mutated or demoted original verified parent: %+v", run, store.candidates[0])
		}
		if run == 0 && !adapter.childWasInitiallyUnverified {
			t.Fatalf("normalized child was not independently unverified before execution: %+v", store.candidates)
		}
	}
	if provider.calls != 0 {
		t.Fatalf("model calls=%d", provider.calls)
	}
	if len(store.candidates) != 2 {
		t.Fatalf("expected deterministic parent+child dedupe, candidates=%d", len(store.candidates))
	}
	child := store.candidates[1]
	if child.ID == parent.ID || child.Status != "upstream_verified" || child.Source != "editor_newline_normalized" || child.PromptVersion != "crlf_to_lf_v1" || child.ParentCandidateID != parent.ID || child.Provider != parent.Provider || child.Model != parent.Model {
		t.Fatalf("unexpected normalized child metadata: %+v", child)
	}
	if child.Files[0].Content != newlineCandidateLF || len(child.Validations) != 4 {
		t.Fatalf("child was not independently self/formal validated: %+v", child)
	}
	if store.candidates[0].Files[0].Content != newlineCandidateCRLF || store.candidates[0].Status != "upstream_verified" || len(store.candidates[0].Validations) != 1 {
		t.Fatalf("original parent/evidence changed: %+v", store.candidates[0])
	}
	for _, validation := range child.Validations {
		if validation.FilesHash != child.FilesHash || validation.SavedFilesHash != child.FilesHash || validation.Kind == "formal" && validation.SubmittedFilesHash != child.FilesHash {
			t.Fatalf("child validation did not bind LF hash: %+v child=%s", validation, child.FilesHash)
		}
	}
}

func TestSingleFileLFParentDoesNotCreateDerivative(t *testing.T) {
	files := []problem.SolutionFile{{Path: "main.cpp", Content: newlineCandidateLF}}
	hash, _ := problem.HashFiles(files)
	parent := verifiedNewlineBankParent(t)
	parent.FilesHash = hash
	parent.Files = files
	parent.CandidateInput.Files = files
	for i := range parent.Validations {
		parent.Validations[i].FilesHash = hash
		parent.Validations[i].SavedFilesHash = hash
		parent.Validations[i].SubmittedFilesHash = hash
	}
	store := &fakeStore{candidates: []questionbank.Candidate{parent}}
	adapter := &newlineCheckingAdapter{fakeAdapter: fakeAdapter{snap: newlineBankSnapshot("single_file")}, store: store}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(store, nil, Config{Timeout: time.Second}).Run(context.Background(), adapter, req)
	// The exact LF bytes are already formally verified: no derivative, no
	// repeated public sample, one formal evaluation.
	if err != nil || result.Status != "passed" || len(store.candidates) != 1 || adapter.selfTests != 0 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v candidates=%d self=%d submit=%d", result, err, len(store.candidates), adapter.selfTests, adapter.submits)
	}
}

func TestRejectedNormalizedDerivativeCannotInheritLegacyRetest(t *testing.T) {
	parent := legacyRejectedCandidate(t, "ok\r\n", "ok")
	parent.ID = 2580
	parent.Files = []problem.SolutionFile{{Path: "main.cpp", Content: newlineCandidateCRLF}}
	parent.CandidateInput.Files = parent.Files
	parent.FilesHash, _ = problem.HashFiles(parent.Files)
	for i := range parent.Validations {
		parent.Validations[i].CandidateID = parent.ID
		parent.Validations[i].FilesHash = parent.FilesHash
		parent.Validations[i].SavedFilesHash = parent.FilesHash
	}
	childFiles := []problem.SolutionFile{{Path: "main.cpp", Content: newlineCandidateLF}}
	childHash, _ := problem.HashFiles(childFiles)
	parent.Ref = questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	rejectedChild := questionbank.Candidate{ID: 2581, Ref: parent.Ref, CandidateInput: questionbank.CandidateInput{Files: childFiles, Source: "editor_newline_normalized", ParentCandidateID: parent.ID, PromptVersion: "crlf_to_lf_v1"}, FilesHash: childHash, Status: "rejected"}
	store := &fakeStore{candidates: []questionbank.Candidate{parent, rejectedChild}}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate(newlineCandidateLF)}}
	adapter := &newlineCheckingAdapter{fakeAdapter: fakeAdapter{snap: newlineBankSnapshot("single_file")}, store: store}
	req := recoveryRequest()
	req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 1, 2
	result, err := New(store, provider, Config{Timeout: time.Second}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "generation_stalled" || result.ModelCalls != 0 || provider.calls != 0 || adapter.selfTests != 0 || adapter.submits != 0 {
		t.Fatalf("rejected LF derivative was retried: result=%+v err=%v calls=%d self=%d submit=%d", result, err, provider.calls, adapter.selfTests, adapter.submits)
	}
	if len(store.candidates) != 2 || store.candidates[0].Status != "rejected" || store.candidates[1].Status != "rejected" {
		t.Fatalf("rejection was changed or duplicate derivative added: %+v", store.candidates)
	}
}

func TestSingleFileModelOutputNormalizedBeforeBankHashAndExecution(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate(newlineCandidateCRLF)}}
	adapter := &newlineCheckingAdapter{fakeAdapter: fakeAdapter{snap: newlineBankSnapshot("single_file")}, store: store}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(store, provider, Config{Timeout: time.Second}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "passed" || result.ModelCalls != 1 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v calls=%d self=%d submit=%d", result, err, provider.calls, adapter.selfTests, adapter.submits)
	}
	if len(store.candidates) != 1 || store.candidates[0].Files[0].Content != newlineCandidateLF || store.candidates[0].Source != "model" || store.candidates[0].ParentCandidateID != 0 || result.CodeHash != store.candidates[0].FilesHash {
		t.Fatalf("model LF source/hash not bound before initial bank write: result=%+v candidates=%+v", result, store.candidates)
	}
	if provider.candidates[0].Files[0].Content != newlineCandidateCRLF {
		t.Fatalf("normalizer mutated provider's original candidate: %q", provider.candidates[0].Files[0].Content)
	}
}

type observedNewlineStore struct {
	*questionbank.Store
	t           *testing.T
	parentID    int64
	derivations int
}

func (s *observedNewlineStore) AddCandidate(ctx context.Context, ref questionbank.Ref, input questionbank.CandidateInput) (questionbank.Candidate, error) {
	c, err := s.Store.AddCandidate(ctx, ref, input)
	if err == nil && input.Source == "editor_newline_normalized" {
		s.derivations++
		if input.ParentCandidateID != s.parentID || c.Status != "candidate" || len(c.Validations) != 0 {
			s.t.Fatalf("new SQLite derivative inherited trust: input=%+v candidate=%+v", input, c)
		}
	}
	return c, err
}

func TestSingleFileSQLiteDerivativePersistsLFProofAndRanksVerifiedChildFirst(t *testing.T) {
	ctx := context.Background()
	bank, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer bank.Close()
	snap, err := problem.Prepare(newlineBankSnapshot("single_file"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := bank.UpsertSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := bank.AddCandidate(ctx, ref, verifiedNewlineBankParent(t).CandidateInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := bank.RecordValidation(ctx, operatorValidation(parent, "formal", "passed")); err != nil {
		t.Fatal(err)
	}
	parent, err = bank.Candidate(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	store := &observedNewlineStore{Store: bank, t: t, parentID: parent.ID}
	var childID int64
	for run := 0; run < 2; run++ {
		adapter := &newlineCheckingAdapter{fakeAdapter: fakeAdapter{snap: snap}, store: &fakeStore{}}
		req := recoveryRequest()
		req.Mode, req.IdempotencyKey = ModeSubmit, "sqlite-newline-"+string(rune('1'+run))
		result, err := New(store, nil, Config{Timeout: time.Second}).Run(ctx, adapter, req)
		// Run 1 creates and proves the LF child; run 2 reuses the proven child
		// directly, so only the first run executes the public sample.
		wantSelfTests := 1
		if run == 1 {
			wantSelfTests = 0
		}
		if err != nil || result.Status != "passed" || result.ModelCalls != 0 || adapter.selfTests != wantSelfTests || adapter.submits != 1 || result.CandidateID == parent.ID {
			t.Fatalf("SQLite run %d result=%+v err=%v self=%d submit=%d", run, result, err, adapter.selfTests, adapter.submits)
		}
		if run == 0 {
			childID = result.CandidateID
		} else if result.CandidateID != childID {
			t.Fatalf("second run used another candidate: first=%d result=%+v", childID, result)
		}
		record, err := bank.GetRun(ctx, result.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var state runState
		if err := json.Unmarshal(record.Data, &state); err != nil {
			t.Fatal(err)
		}
		if state.LastCandidate == nil || state.LastCandidate.Files[0].Content != newlineCandidateLF || state.Result.CandidateID != childID || state.Result.CodeHash != result.CodeHash {
			t.Fatalf("checkpoint does not bind LF child: %+v", state)
		}
	}
	lookup, err := bank.Lookup(ctx, ref)
	if err != nil || len(lookup) != 2 || lookup[0].ID != childID || lookup[0].Status != "upstream_verified" || store.derivations != 1 {
		t.Fatalf("verified child not reused first: lookup=%+v derivations=%d err=%v", lookup, store.derivations, err)
	}
	for _, validation := range lookup[0].Validations {
		if validation.CandidateID != childID || validation.FilesHash != lookup[0].FilesHash || validation.SavedFilesHash != lookup[0].FilesHash || validation.Kind == "formal" && validation.SubmittedFilesHash != lookup[0].FilesHash {
			t.Fatalf("SQLite validation did not bind LF child: %+v", validation)
		}
	}
	unchanged, err := bank.Candidate(ctx, parent.ID)
	if err != nil || !reflect.DeepEqual(parent, unchanged) {
		t.Fatalf("historical CRLF parent/evidence changed: before=%+v after=%+v err=%v", parent, unchanged, err)
	}
}

func TestCRLFNormalizationIsLimitedToSingleFileModeAndCRLFSequences(t *testing.T) {
	for _, mode := range []string{"terminal", "graphic", "multi_file", ""} {
		file := answer.Candidate{Files: []problem.SolutionFile{{Path: "main.cpp", Content: newlineCandidateCRLF}}}
		normalized, changed := normalizeSingleFileCRLF(newlineBankSnapshot(mode), file)
		if changed || normalized.Files[0].Content != newlineCandidateCRLF {
			t.Fatalf("mode %q changed candidate: %+v changed=%v", mode, normalized, changed)
		}
	}
	input := "\ufeffa  \r\nb\rc\n\ufeffd\n\n"
	file := answer.Candidate{Files: []problem.SolutionFile{{Path: "main.cpp", Content: input}}}
	got, changed := normalizeSingleFileCRLF(newlineBankSnapshot("single_file"), file)
	if !changed || got.Files[0].Content != strings.ReplaceAll(input, "\r\n", "\n") {
		t.Fatalf("normalizer changed bytes outside CRLF pairs: %q changed=%v", got.Files[0].Content, changed)
	}
	if file.Files[0].Content != input {
		t.Fatalf("normalizer mutated original answer candidate: %q", file.Files[0].Content)
	}
	for _, content := range []string{"\ufeffa  \nb\rc\n\n", "no final newline", "\n\n", ""} {
		file.Files[0].Content = content
		got, changed = normalizeSingleFileCRLF(newlineBankSnapshot("single_file"), file)
		if changed || got.Files[0].Content != content {
			t.Fatalf("non-CRLF source changed: input=%q output=%q changed=%v", content, got.Files[0].Content, changed)
		}
	}
}
