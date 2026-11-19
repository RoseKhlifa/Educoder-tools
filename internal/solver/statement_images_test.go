package solver

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestStatementImageLocatorsKeepStatementOrderAndDeduplicate(t *testing.T) {
	statement := "先看 <img alt=\"a\" src=\"/api/attachments/2?x=1&amp;y=2\"> 再看 ![b](/api/attachments/1 \"title\") 与 ![c](</api/attachments/3>) 重复 ![d](/api/attachments/1) <img src='/api/attachments/4'/>"
	got := statementImageLocators(statement)
	want := []string{"/api/attachments/2?x=1&amp;y=2", "/api/attachments/1", "/api/attachments/3", "/api/attachments/4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locators=%q want %q", got, want)
	}
	if len(statementImageLocators("纯文本题面，没有图片")) != 0 {
		t.Fatal("text-only statement produced locators")
	}
}

type figureAdapter struct {
	fakeAdapter
	figureReads int
	images      []answer.StatementImage
	unavailable []string
}

func (a *figureAdapter) StatementImages(context.Context, Target, problem.Snapshot) ([]answer.StatementImage, []string) {
	a.figureReads++
	return a.images, a.unavailable
}

func TestModelGenerationReceivesStatementFiguresOncePerRun(t *testing.T) {
	snap := testSnapshot()
	snap.StatementMarkdown = "按图 ![图](/api/attachments/7) 输出 ![缺](/api/attachments/9)"
	adapter := &figureAdapter{fakeAdapter: fakeAdapter{snap: snap}, images: []answer.StatementImage{{Locator: "/api/attachments/7", MediaType: "image/png", SHA256: "x"}}, unavailable: []string{"/api/attachments/9"}}
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if len(req.Images) != 1 || req.Images[0].Locator != "/api/attachments/7" || len(req.ImagesUnavailable) != 1 {
			t.Fatalf("call %d missing statement figures: images=%d unavailable=%v", n, len(req.Images), req.ImagesUnavailable)
		}
		if n == 1 {
			return solved("bad", 2), nil
		}
		return solved("good", 2), nil
	}}
	r, err := New(store, p, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 2 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if adapter.figureReads != 1 {
		t.Fatalf("figures read %d times; want once per run", adapter.figureReads)
	}
	found := false
	for _, d := range r.Diagnostics {
		found = found || d == "题干图片有 1 张未能读取，已按其余题面信息生成"
	}
	if !found {
		t.Fatalf("unavailable figure not reported: %v", r.Diagnostics)
	}
}

func TestBankCandidateDoesNotReadStatementFigures(t *testing.T) {
	snap := testSnapshot()
	snap.StatementMarkdown = "按图 ![图](/api/attachments/7)"
	adapter := &figureAdapter{fakeAdapter: fakeAdapter{snap: snap}}
	files := []problem.SolutionFile{{Path: "main.cpp", Content: "good"}}
	hash, _ := problem.HashFiles(files)
	store := &fakeStore{}
	store.candidates = append(store.candidates, verifiedCandidateForTest(1, files, hash))
	r, err := New(store, &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		t.Fatal("bank hit called the model")
		return answer.Result{}, nil
	}}, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.ModelCalls != 0 || adapter.figureReads != 0 {
		t.Fatalf("result=%+v err=%v figureReads=%d", r, err, adapter.figureReads)
	}
}

// verifiedCandidateForTest builds a bank candidate already proven by a
// complete formal evaluation under the fake store's snapshot reference.
func verifiedCandidateForTest(id int64, files []problem.SolutionFile, hash string) questionbank.Candidate {
	ref := questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	return questionbank.Candidate{ID: id, Ref: ref, CandidateInput: questionbank.CandidateInput{Files: files, Source: "model"}, FilesHash: hash, Status: "upstream_verified",
		Validations: []questionbank.Validation{{CandidateID: id, Kind: "formal", Result: "passed", FilesHash: hash, SavedFilesHash: hash, SubmittedFilesHash: hash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}}}
}

func TestEducoderAdapterReadsStatementFiguresThroughGuardedAttachmentPath(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	pngBytes := buf.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/attachments/7":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/api/attachments/8":
			_, _ = w.Write([]byte("not an image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter, err := NewEducoderAdapter(educoder.New(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	snap := testSnapshot()
	snap.StatementMarkdown = "![a](/api/attachments/7) ![b](/api/attachments/8) ![c](/api/attachments/9) ![d](https://elsewhere.example/x.png)"
	images, unavailable := adapter.StatementImages(context.Background(), Target{}, snap)
	if len(images) != 1 || images[0].Locator != "/api/attachments/7" || images[0].MediaType != "image/png" || len(images[0].Content) != len(pngBytes) || images[0].SHA256 == "" {
		t.Fatalf("images=%+v", images)
	}
	want := []string{"/api/attachments/8", "/api/attachments/9", "https://elsewhere.example/x.png"}
	if !reflect.DeepEqual(unavailable, want) {
		t.Fatalf("unavailable=%q want %q", unavailable, want)
	}
	// The fetched set is accepted by the provider contract as-is.
	req := answer.Request{Problem: snap, Profile: answer.DefaultProfile(), Images: images, ImagesUnavailable: unavailable}
	provider, err := answer.NewDeepSeek(answer.Config{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Estimate(req); err != nil {
		t.Fatalf("adapter output rejected by provider contract: %v", err)
	}
}
