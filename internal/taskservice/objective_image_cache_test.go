package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// The source and target accounts see the same diagram at different signed URLs.
// The target must reuse the proven answer after fetching the actual diagram.
func TestObjectiveImageCacheCrossAccount(t *testing.T) {
	store := bank(t)
	var submits, images atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/tasks/") && strings.HasSuffix(r.URL.Path, "/choose_build.json"):
			submits.Add(1)
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
		case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
			var task educoder.Task
			if err := json.Unmarshal([]byte(objectiveJSON()), &task); err != nil {
				t.Error(err)
				return
			}
			game := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), ".json")
			task.Game.Identifier = game
			task.Chooses[0].Subject = `<img src="/api/attachments/diagram?signature=` + game + `"> Which traversal matches the diagram?`
			if game == "CHANGED" {
				task.Chooses[0].Subject = `<img src="/api/attachments/changed?signature=CHANGED"> Which traversal matches the diagram?`
			}
			json.NewEncoder(w).Encode(task)
		case strings.HasPrefix(r.URL.Path, "/api/attachments/"):
			images.Add(1)
			w.Header().Set("Content-Type", "image/png")
			value := byte(1)
			if strings.HasSuffix(r.URL.Path, "/changed") {
				value = 2
			}
			w.Write(objectiveResourcePNG(t, value))
		case r.URL.Path == "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":"13.30","passed_status":2}]}}`)
		default:
			t.Errorf("unexpected image-cache endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := educoder.New(server.URL)
	provider := &fakeProvider{}
	source := req(solver.ModeSubmit)
	source.Owner, source.Game = "source-account", "SOURCE"
	first, err := New(store, provider, Config{}).Run(context.Background(), client, source, nil)
	if err != nil || first == nil || first.Status != "passed" || first.ModelCalls != 1 {
		t.Fatalf("source did not establish full-score image evidence: result=%+v err=%v", first, err)
	}
	// A distinct account/runtime sees the same content through its own image URL.
	target := req(solver.ModeSubmit)
	target.Owner, target.Game = "target-account", "TARGET"
	second, err := New(store, nil, Config{}).Run(context.Background(), client, target, nil)
	if err != nil || second == nil || second.Status != "passed" || second.Source != "bank" || second.ModelCalls != 0 || second.CandidateID != first.CandidateID {
		t.Fatalf("identical image cache missed for target account: result=%+v err=%v", second, err)
	}
	// Each formal submission re-fetches the bytes before writing, so a changed
	// signed image cannot inherit a previously prepared snapshot's answer.
	if provider.objectiveCalls.Load() != 1 || submits.Load() != 2 || images.Load() != 4 {
		t.Fatalf("unexpected work: model=%d submissions=%d image_reads=%d", provider.objectiveCalls.Load(), submits.Load(), images.Load())
	}
	changed := req(solver.ModeGenerateOnly)
	changed.Owner, changed.Game = "target-account", "CHANGED"
	third, err := New(store, nil, Config{}).Run(context.Background(), client, changed, nil)
	if err == nil || third == nil || third.Status != "needs_provider" || submits.Load() != 2 {
		t.Fatalf("changed image reused old answer: result=%+v err=%v submissions=%d", third, err, submits.Load())
	}
}
