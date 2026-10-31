package taskservice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

func objectiveResourceFixture() *educoder.ChoiceTask {
	task := &educoder.Task{}
	task.Challenge.Subject = "tree questions"
	task.Challenge.Description = "select the correct diagram"
	return &educoder.ChoiceTask{Task: task, Questions: []educoder.ChoiceQuestion{
		{Position: 5, Subject: "choose the tree", Category: 1, Options: []educoder.ChoiceOption{{Position: 0, OptionName: "first"}, {Position: 1, OptionName: "second"}}},
		{Position: 7, Subject: "choose the diagram", Category: 1, Options: []educoder.ChoiceOption{{Position: 0, OptionName: "A"}, {Position: 1, OptionName: "B"}, {Position: 2, OptionName: "C"}, {Position: 3, OptionName: "D"}}},
	}}
}

func objectiveResourcePNG(t *testing.T, value byte) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	im.Set(0, 0, color.RGBA{R: value, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, im); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPrepareObjectiveResourcesBindsStemAndOptionsWithoutPersistingURLs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasPrefix(r.URL.Path, "/api/attachments/") {
			t.Error("unexpected image path")
		}
		w.Write(objectiveResourcePNG(t, r.URL.Path[len(r.URL.Path)-1]))
	}))
	defer server.Close()
	choice := objectiveResourceFixture()
	choice.Questions[0].Subject = `<img src="/api/attachments/1?type=image&amp;signature=fixture-private">`
	for i := range choice.Questions[1].Options {
		choice.Questions[1].Options[i].OptionName = "![option](/api/attachments/" + string(rune('2'+i)) + "?signature=fixture-private)"
	}
	snapshot, resources, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 5 || len(resources) != 5 || resources[0].QuestionPosition != 5 || resources[0].OptionLabel != "" {
		t.Fatalf("wrong image count or stem binding: requests=%d resources=%d", requests, len(resources))
	}
	for i := 1; i < 5; i++ {
		if resources[i].QuestionPosition != 7 || resources[i].OptionLabel != string(rune('A'+i-1)) {
			t.Fatal("option image bound to the wrong question or option")
		}
	}
	encoded, _ := json.Marshal(snapshot)
	for _, secret := range []string{"signature", "fixture-private", "/api/attachments/", server.URL} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("snapshot retained an image locator")
		}
	}
	if strings.Count(string(encoded), "sha256:") != 5 {
		t.Fatal("snapshot did not retain all content-hash placeholders")
	}
	if err := answer.ValidateObjectiveResources(snapshot, resources); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(choice.Questions[0].Subject, "fixture-private") {
		t.Fatal("preparing snapshot mutated the original task")
	}
}

func TestPrepareObjectiveResourcesNoImagesUnchanged(t *testing.T) {
	choice := objectiveResourceFixture()
	snapshot, resources, err := prepareObjectiveResources(context.Background(), nil, choice)
	if err != nil || len(resources) != 0 || !reflect.DeepEqual(snapshot, objectiveSnapshot(choice)) {
		t.Fatalf("text-only objective changed: %v", err)
	}
}

func TestPrepareObjectiveResourcesFailsClosed(t *testing.T) {
	for _, markup := range []string{`![image][reference]`, `<svg><path/></svg>`, `<video src="/api/attachments/1"></video>`, `<img data-src="/api/attachments/1">`, `<img srcset="/api/attachments/1 2x" src="/api/attachments/1">`, `<img src="data:image/png;base64,aGVsbG8=">`, `<img src="https://evil.example/image.png">`, `<img src="/api/attachments/1" src="/api/attachments/2">`, `<img src="/api/attachments/1"`} {
		choice := objectiveResourceFixture()
		choice.Questions[0].Subject = markup
		snapshot, resources, err := prepareObjectiveResources(context.Background(), educoder.New(""), choice)
		var typed *educoder.QuestionImageError
		if !errors.As(err, &typed) || len(resources) != 0 || snapshot.Kind != "" {
			t.Fatal("unsupported media did not fail closed")
		}
	}
}

func TestPrepareObjectiveResourcesRejectsCorruptImageAndUnavailableResponse(t *testing.T) {
	for _, content := range [][]byte{nil, []byte("<html>not found</html>"), []byte("\x89PNG\r\n\x1a\ncorrupt")} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
		choice := objectiveResourceFixture()
		choice.Questions[0].Subject = `<img src="/api/attachments/1">`
		_, _, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
		server.Close()
		if err == nil {
			t.Fatal("invalid image did not prevent an image-blind model request")
		}
	}
}

func TestPrepareObjectiveResourcesSharedImagesUseMemoryCacheAndCountLimit(t *testing.T) {
	requests := 0
	content := objectiveResourcePNG(t, 42)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write(content) }))
	defer server.Close()
	choice := objectiveResourceFixture()
	choice.Task.Challenge.Description = `<img src="/api/attachments/shared?signature=fixture-private">`
	choice.Questions[0].Subject = `![repeat](/api/attachments/shared?signature=fixture-private)`
	snapshot, resources, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
	if err != nil || requests != 1 || len(resources) != 3 || resources[0].QuestionPosition != 5 || resources[2].QuestionPosition != 7 {
		t.Fatalf("shared resource cache failed: requests=%d resources=%d err=%v", requests, len(resources), err)
	}
	if err := answer.ValidateObjectiveResources(snapshot, resources); err != nil {
		t.Fatal(err)
	}
	choice = objectiveResourceFixture()
	choice.Questions[0].Subject = strings.Repeat(`![repeat](/api/attachments/shared)`, 129)
	if _, _, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice); err == nil {
		t.Fatal("image-count limit not enforced")
	}
}

func TestPrepareObjectiveResourcesRedactsLocatorInCaption(t *testing.T) {
	content := objectiveResourcePNG(t, 15)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer server.Close()
	choice := objectiveResourceFixture()
	locator := "/api/attachments/1?signature=fixture-private"
	choice.Questions[0].Subject = `<a href="` + locator + `"><img src="` + locator + `"></a> source ` + locator
	snapshot, _, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
	if err != nil || strings.Contains(snapshot.Questions[0].Question, "fixture-private") {
		t.Fatal("image locator retained in surrounding caption")
	}
}

func TestPrepareObjectiveResourcesAggregateByteLimit(t *testing.T) {
	// Reusing one near-10MiB image avoids multiple allocations/downloads while
	// still checking the provider's aggregate bound for five attachments.
	content := objectiveResourcePNG(t, 35)
	content = append(content, make([]byte, educoder.MaxQuestionImageBytes-1-len(content))...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer server.Close()
	choice := objectiveResourceFixture()
	choice.Questions[0].Subject = strings.Repeat(`![same](/api/attachments/large)`, 5)
	_, _, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
	var typed *educoder.QuestionImageError
	if !errors.As(err, &typed) || typed.Kind != "objective_image_limit" {
		t.Fatalf("aggregate size bound not enforced: %v", err)
	}
}

type objectiveResourceProvider struct {
	fakeProvider
	estimated []answer.ObjectiveRequest
	generated []answer.ObjectiveRequest
}

func (p *objectiveResourceProvider) EstimateObjective(request answer.ObjectiveRequest) (answer.Estimate, error) {
	p.estimated = append(p.estimated, request)
	return p.estimate(), nil
}

func (p *objectiveResourceProvider) GenerateObjective(_ context.Context, request answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.generated = append(p.generated, request)
	return answer.ObjectiveResult{Candidate: answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "B"}}, Model: "fake", CostKnown: true, CostMicroUSD: 2}, nil
}

func TestObjectiveServicePassesAllImageBytesToEstimateAndGenerateAndPersistsOnlyHashes(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "all-images-loaded", true: "image-unavailable"}[unavailable], func(t *testing.T) {
			fixture := objectiveResourceFixture()
			fixture.Questions[0].Subject = `<img src="/api/attachments/1?signature=fixture-private">`
			for i := range fixture.Questions[1].Options {
				fixture.Questions[1].Options[i].OptionName = "![option](/api/attachments/" + string(rune('2'+i)) + "?signature=fixture-private)"
			}
			var task educoder.Task
			if err := json.Unmarshal([]byte(objectiveJSON()), &task); err != nil {
				t.Fatal(err)
			}
			task.Chooses = fixture.Questions
			var imageCalls, submits atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/tasks/OBJGAME.json":
					json.NewEncoder(w).Encode(task)
				case strings.HasPrefix(r.URL.Path, "/api/attachments/"):
					imageCalls.Add(1)
					if unavailable {
						http.NotFound(w, r)
						return
					}
					w.Write(objectiveResourcePNG(t, r.URL.Path[len(r.URL.Path)-1]))
				case strings.HasSuffix(r.URL.Path, "choose_build.json"):
					submits.Add(1)
					t.Error("generate-only resource test attempted submission")
					http.Error(w, "not expected", 500)
				default:
					t.Error("unexpected service endpoint")
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			dbPath := filepath.Join(t.TempDir(), "resource-test.sqlite")
			store, err := questionbank.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			provider := &objectiveResourceProvider{}
			result, runErr := New(store, provider, Config{}).Run(context.Background(), educoder.New(server.URL), req(solver.ModeGenerateOnly), nil)
			if submits.Load() != 0 {
				t.Fatal("resource generation submitted an answer")
			}
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if unavailable {
				var typed *educoder.QuestionImageError
				if !errors.As(runErr, &typed) || result == nil || result.Status != "resource_unavailable" || len(provider.estimated) != 0 || len(provider.generated) != 0 || imageCalls.Load() != 1 {
					t.Fatalf("image failure did not stop before provider: status=%v err=%v", result, runErr)
				}
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM objective_snapshots`).Scan(&count); err != nil || count != 0 {
					t.Fatal("unprepared image snapshot was persisted")
				}
				return
			}
			if runErr != nil || result == nil || result.Status != "generated" || len(provider.estimated) != 1 || len(provider.generated) != 1 || imageCalls.Load() != 5 {
				t.Fatalf("image request did not reach model: status=%v err=%v", result, runErr)
			}
			for _, request := range []answer.ObjectiveRequest{provider.estimated[0], provider.generated[0]} {
				if len(request.Resources) != 5 || request.Profile.Thinking != "disabled" || request.Profile.MaxTokens != 8192 {
					t.Fatal("provider received missing images or a reasoning-enabled profile")
				}
				for i, resource := range request.Resources {
					position, label := 5, ""
					if i > 0 {
						position, label = 7, string(rune('A'+i-1))
					}
					if resource.QuestionPosition != position || resource.OptionLabel != label || !bytes.Equal(resource.Content, objectiveResourcePNG(t, byte('1'+i))) || len(resource.SHA256) != 64 {
						t.Fatal("provider image bytes or question/option mapping changed")
					}
				}
				if err := answer.ValidateObjectiveResources(request.Problem, request.Resources); err != nil {
					t.Fatal(err)
				}
				publicJSON, _ := json.Marshal(request)
				if bytes.Contains(publicJSON, []byte("fixture-private")) || bytes.Contains(publicJSON, []byte("/api/attachments/")) {
					t.Fatal("provider snapshot retained raw signed URL")
				}
			}
			var persisted string
			if err := db.QueryRow(`SELECT snapshot_json FROM objective_snapshots`).Scan(&persisted); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(persisted, "fixture-private") || strings.Contains(persisted, "/api/attachments/") || strings.Count(persisted, "sha256:") != 5 {
				t.Fatal("persisted snapshot retained a raw image URL or missed image hashes")
			}
			var persistedSnapshot answer.ObjectiveSnapshot
			if err := json.Unmarshal([]byte(persisted), &persistedSnapshot); err != nil || !reflect.DeepEqual(persistedSnapshot, provider.generated[0].Problem) {
				t.Fatal("persisted snapshot differs from sanitized model snapshot")
			}
		})
	}
}
