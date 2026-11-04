package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
)

type commonCatalogClient struct {
	*fakeClient
	catalog     func(context.Context, string, string) ([]educoder.Homework, error)
	legacyReads atomic.Int32
}

func (f *commonCatalogClient) Homeworks(ctx context.Context, course, category string) ([]educoder.Homework, error) {
	f.legacyReads.Add(1)
	return f.fakeClient.Homeworks(ctx, course, category)
}

func (f *commonCatalogClient) HomeworksForDisplay(ctx context.Context, course, category string) ([]educoder.Homework, error) {
	return f.catalog(ctx, course, category)
}

func commonCatalogHTTPClient(t *testing.T, fake EducoderClient) (*httptest.Server, *http.Client) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.StaticDir = ""
	srv := httptest.NewServer(NewServerWithFactory(cfg, func(string) EducoderClient { return fake }).Handler())
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login := doJSON(t, client, http.MethodPost, srv.URL+"/api/auth/login", `{"login":"fixture","password":"fixture-password"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %s", login.Body)
	}
	return srv, client
}

func TestHomeworkListPrefersCompleteCatalog(t *testing.T) {
	var calls atomic.Int32
	fake := &commonCatalogClient{fakeClient: &fakeClient{}, catalog: func(_ context.Context, course, category string) ([]educoder.Homework, error) {
		calls.Add(1)
		if course != "COURSE1" || category != "category-1" {
			t.Errorf("wrong catalog request: course=%q category=%q", course, category)
		}
		return []educoder.Homework{
			{ID: 42, Name: "实验作业", Kind: "shixun", IsShixun: true, ChallengeCount: 1},
			{ID: 3968749, Name: "图文作业", Kind: "common", Status: []string{"提交中"}, EndTime: "2099-11-11 08:30:00"},
		}, nil
	}}
	srv, client := commonCatalogHTTPClient(t, fake)
	response := doJSON(t, client, http.MethodGet, srv.URL+"/api/courses/COURSE1/homeworks?category=category-1", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("homeworks status=%d: %s", response.StatusCode, response.Body)
	}
	var envelope struct {
		Data []homeworkDTO `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || fake.legacyReads.Load() != 0 || len(envelope.Data) != 2 {
		t.Fatalf("incomplete catalog used: calls=%d legacy=%d body=%s", calls.Load(), fake.legacyReads.Load(), response.Body)
	}
	common := envelope.Data[1]
	if common.ID != 3968749 || common.HomeworkKind != "common" || common.AutomaticSupported || common.IsShixun || common.Deadline == "" || common.RequiredKnown {
		t.Fatalf("common homework classification lost: %s", response.Body)
	}
	if envelope.Data[0].HomeworkKind != "shixun" || !envelope.Data[0].AutomaticSupported {
		t.Fatalf("shixun execution support lost: %s", response.Body)
	}
}

func TestHomeworkListDoesNotClaimCompleteCatalogAfterPartialFailure(t *testing.T) {
	fake := &commonCatalogClient{fakeClient: &fakeClient{homeworks: []educoder.Homework{{ID: 1, Name: "old shixun-only catalog"}}}, catalog: func(context.Context, string, string) ([]educoder.Homework, error) {
		return []educoder.Homework{{ID: 1, Kind: "shixun", IsShixun: true}}, errors.New("common-homework tab unavailable")
	}}
	srv, client := commonCatalogHTTPClient(t, fake)
	response := doJSON(t, client, http.MethodGet, srv.URL+"/api/courses/COURSE1/homeworks", "")
	if response.StatusCode != http.StatusBadGateway || fake.legacyReads.Load() != 0 {
		t.Fatalf("partial tab failure was hidden: status=%d legacy=%d body=%s", response.StatusCode, fake.legacyReads.Load(), response.Body)
	}
}

func TestCommonHomeworkSkipsShixunProgressAndOptionalFiltering(t *testing.T) {
	var reads atomic.Int32
	fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(_ context.Context, _ string, id int64) (*educoder.HomeworkProgress, error) {
		reads.Add(1)
		if id != 2 {
			t.Errorf("common homework sent to shixun progress endpoint: %d", id)
		}
		return optionalListProgress(1, 0), nil
	}}
	homeworks := []educoder.Homework{
		{ID: 3968749, Kind: "common", Name: "图文作业", Status: []string{"提交中"}, ChallengeCount: 1},
		{ID: 2, Kind: "shixun", IsShixun: true, ChallengeCount: 1},
	}
	result := serviceHomeworks(context.Background(), fake, "COURSE1", homeworks)
	if reads.Load() != 1 || len(result) != 1 || result[0].ID != 3968749 || result[0].RequiredKnown || result[0].RequiredCount != nil || result[0].FinishedRequired != nil {
		t.Fatalf("common assignment was dropped or given shixun progress: reads=%d result=%+v", reads.Load(), result)
	}
}

func TestCommonHomeworksDoNotConsumeShixunEnrichmentLimit(t *testing.T) {
	var reads atomic.Int32
	fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
		reads.Add(1)
		return optionalListProgress(1, 1), nil
	}}
	homeworks := make([]educoder.Homework, 101)
	for i := range homeworks[:100] {
		homeworks[i] = educoder.Homework{ID: int64(i + 1), Kind: "common"}
	}
	homeworks[100] = educoder.Homework{ID: 101, Kind: "shixun", IsShixun: true, ChallengeCount: 1}
	result := serviceHomeworks(context.Background(), fake, "COURSE1", homeworks)
	if reads.Load() != 1 || len(result) != 101 || !result[100].RequiredKnown {
		t.Fatalf("common rows exhausted progress enrichment budget: reads=%d result=%+v", reads.Load(), result)
	}
}

func TestLegacyHomeworkClientRetainsShixunExecutionSupport(t *testing.T) {
	result := toHomeworkDTO(educoder.Homework{ID: 42, IsShixun: true})
	if result.HomeworkKind != "shixun" || !result.AutomaticSupported {
		t.Fatalf("legacy shixun row lost executable classification: %+v", result)
	}
}

func TestCommonHomeworkSubmissionStatusUsesExplicitUpstreamState(t *testing.T) {
	for _, test := range []struct {
		name        string
		uncommitted *bool
		want        string
	}{
		{name: "not submitted", uncommitted: boolPointer(true), want: "not_submitted"},
		{name: "submitted", uncommitted: boolPointer(false), want: "submitted"},
		{name: "unknown", want: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := toHomeworkDTO(educoder.Homework{
				ID:            3968749,
				Kind:          "common",
				Status:        []string{"提交中"},
				StudentWorkID: 304751696,
				Uncommitted:   test.uncommitted,
			})
			if result.SubmissionStatus != test.want || result.AutomaticSupported || result.RequiredKnown || result.RequiredCount != nil || result.FinishedRequired != nil {
				t.Fatalf("common submission state misclassified: %+v", result)
			}
		})
	}
	shixun := toHomeworkDTO(educoder.Homework{Kind: "shixun", IsShixun: true})
	if shixun.SubmissionStatus != "" {
		t.Fatalf("common submission state leaked into shixun DTO: %+v", shixun)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
