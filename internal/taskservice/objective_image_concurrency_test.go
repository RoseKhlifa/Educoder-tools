package taskservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

func TestObjectiveFiguresDownloadConcurrentlyWithSequentialResult(t *testing.T) {
	choice := objectiveResourceFixture()
	choice.Questions[0].Subject = `<img src="/api/attachments/1?signature=s">`
	for i := range choice.Questions[1].Options {
		choice.Questions[1].Options[i].OptionName = "![o](/api/attachments/" + string(rune('2'+i)) + "?signature=s)"
	}
	var mu sync.Mutex
	inflight, peak, reads := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/attachments/") {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		inflight++
		reads++
		peak = max(peak, inflight)
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		w.Write(objectiveResourcePNG(t, r.URL.Path[len(r.URL.Path)-1]))
		mu.Lock()
		inflight--
		mu.Unlock()
	}))
	defer server.Close()
	snapshot, resources, err := prepareObjectiveResources(context.Background(), educoder.New(server.URL), choice)
	if err != nil {
		t.Fatal(err)
	}
	if peak < 2 || peak > objectiveImageFetchConcurrency || reads != 5 {
		t.Fatalf("figures were not fetched concurrently within bounds: peak=%d reads=%d", peak, reads)
	}
	// Resource order and placeholders are exactly the text order a sequential
	// read produces: question stem first, then options A–D.
	if len(resources) != 5 {
		t.Fatalf("resources=%d", len(resources))
	}
	for i, value := range []byte{'1', '2', '3', '4', '5'} {
		digest := sha256.Sum256(objectiveResourcePNG(t, value))
		if resources[i].SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("resource %d out of text order", i)
		}
	}
	if !strings.Contains(snapshot.Questions[0].Question, "sha256:"+resources[0].SHA256) || strings.Contains(snapshot.Questions[0].Question, "signature") {
		t.Fatalf("placeholder or signed locator mismatch: %q", snapshot.Questions[0].Question)
	}
}
