package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoryNameOnlyEntriesFollowBoundParent(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch body.Path {
		case "":
			fmt.Fprint(w, `{"trees":[{"name":"src","type":"tree"}]}`)
		case "src":
			fmt.Fprint(w, `{"trees":[{"name":"driver.cpp","type":"blob"}]}`)
		default:
			t.Errorf("unobserved directory requested: %q", body.Path)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	root, err := client.ListRepository(context.Background(), task, nil)
	if err != nil || len(root) != 1 || root[0].Path != "src" {
		t.Fatalf("name-only root was not bound to its requested directory: root=%+v err=%v", root, err)
	}
	children, err := client.ListRepository(context.Background(), task, &root[0])
	if err != nil || len(children) != 1 || children[0].Path != "src/driver.cpp" {
		t.Fatalf("name-only child lost the observed parent: children=%+v err=%v", children, err)
	}
	forged := root[0]
	forged.Path = "other"
	if _, err := client.ListRepository(context.Background(), task, &forged); err == nil || requests != 2 {
		t.Fatal("derived path allowed a forged directory or unnecessary request")
	}
}
