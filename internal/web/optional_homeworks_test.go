package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

type optionalProgressClient struct {
	*fakeClient
	read func(context.Context, string, int64) (*educoder.HomeworkProgress, error)
}

func (f *optionalProgressClient) HomeworkProgress(ctx context.Context, course string, id int64) (*educoder.HomeworkProgress, error) {
	return f.read(ctx, course, id)
}

func optionalListProgress(total, required int) *educoder.HomeworkProgress {
	p := &educoder.HomeworkProgress{}
	for i := 1; i <= total; i++ {
		status, score := "required", "16"
		if i > required {
			status, score = "optional", "0"
		}
		p.Challenges = append(p.Challenges, educoder.ChallengeProgress{ChallengeID: int64(i), Name: fmt.Sprintf("关卡%d", i), RequiredStatus: status, GameScore: score, ChallengeScore: score})
	}
	return p
}

func TestHomeworkListUsesVerifiedRequiredProgressAndExcludesAllOptional(t *testing.T) {
	fake := &optionalProgressClient{fakeClient: &fakeClient{homeworks: []educoder.Homework{
		{ID: 1, Name: "五关必做、五关选做", ChallengeCount: 10, FinishedCount: 5},
		{ID: 2, Name: "全选做", ChallengeCount: 2},
		{ID: 3, Name: "进度不完整", ChallengeCount: 3},
		{ID: 4, Name: "零分必做", ChallengeCount: 1},
		{ID: 5, Name: "进度读取失败", ChallengeCount: 1},
	}}}
	fake.read = func(_ context.Context, course string, id int64) (*educoder.HomeworkProgress, error) {
		if course != "COURSE1" {
			t.Errorf("wrong classroom %q", course)
		}
		switch id {
		case 1:
			return optionalListProgress(10, 5), nil
		case 2:
			return optionalListProgress(2, 0), nil
		case 3:
			return optionalListProgress(2, 1), nil
		case 4:
			p := optionalListProgress(1, 1)
			p.Challenges[0].GameScore, p.Challenges[0].ChallengeScore = "0", "0"
			return p, nil
		default:
			return nil, errors.New("upstream response unavailable")
		}
	}
	cfg := DefaultConfig()
	cfg.StaticDir = ""
	srv := httptest.NewServer(NewServerWithFactory(cfg, func(string) EducoderClient { return fake }).Handler())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login := doJSON(t, client, http.MethodPost, srv.URL+"/api/auth/login", `{"login":"fixture","password":"fixture-password"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %s", login.Body)
	}
	response := doJSON(t, client, http.MethodGet, srv.URL+"/api/courses/COURSE1/homeworks", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("homeworks status=%d: %s", response.StatusCode, response.Body)
	}
	var envelope struct {
		Data []homeworkDTO `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 4 || envelope.Data[0].ID != 1 || envelope.Data[1].ID != 3 {
		t.Fatalf("all-optional assignment was not excluded: %s", response.Body)
	}
	first := envelope.Data[0]
	if first.ChallengeCount != 10 || !first.RequiredKnown || first.RequiredCount == nil || *first.RequiredCount != 5 || first.FinishedRequired == nil || *first.FinishedRequired != 5 {
		t.Fatalf("required progress incorrect: %s", response.Body)
	}
	if envelope.Data[1].RequiredKnown || envelope.Data[1].RequiredCount != nil || envelope.Data[3].RequiredKnown || envelope.Data[3].RequiredCount != nil {
		t.Fatalf("unknown progress became a completion claim: %s", response.Body)
	}
	zero := envelope.Data[2]
	if !zero.RequiredKnown || zero.RequiredCount == nil || *zero.RequiredCount != 1 || zero.FinishedRequired == nil || *zero.FinishedRequired != 0 {
		t.Fatalf("zero-score required assignment was excluded or marked complete: %s", response.Body)
	}
}

func TestRequiredProgressRejectsPartialOrAmbiguousRows(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*educoder.HomeworkProgress)
	}{
		{"duplicate id", func(p *educoder.HomeworkProgress) { p.Challenges[1].ChallengeID = p.Challenges[0].ChallengeID }},
		{"duplicate name", func(p *educoder.HomeworkProgress) { p.Challenges[1].Name = p.Challenges[0].Name }},
		{"blank name", func(p *educoder.HomeworkProgress) { p.Challenges[0].Name = " " }},
		{"missing id", func(p *educoder.HomeworkProgress) { p.Challenges[0].ChallengeID = 0 }},
		{"short list", func(p *educoder.HomeworkProgress) { p.Challenges = p.Challenges[:1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := optionalListProgress(2, 1)
			test.mutate(p)
			dto := toHomeworkDTO(educoder.Homework{ChallengeCount: 2})
			applyRequiredProgress(&dto, educoder.Homework{ChallengeCount: 2}, p)
			if dto.RequiredKnown || dto.RequiredCount != nil || dto.FinishedRequired != nil {
				t.Fatalf("ambiguous rows accepted: %+v", dto)
			}
		})
	}
}

func TestHomeworkUnknownOrExcessiveRosterCountDoesNotExcludeOptionalRows(t *testing.T) {
	for _, total := range []int{0, -1, 101} {
		t.Run(fmt.Sprintf("count=%d", total), func(t *testing.T) {
			fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
				return optionalListProgress(1, 0), nil
			}}
			result := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 1, ChallengeCount: total}})
			if len(result) != 1 || result[0].RequiredKnown || result[0].RequiredCount != nil || result[0].FinishedRequired != nil {
				t.Fatalf("unknown roster count became a precise all-optional claim: %+v", result)
			}
		})
	}
}

func TestHomeworkProgressReadsUseFourWorkersAndStopOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 10)
	var reads atomic.Int32
	fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(ctx context.Context, _ string, _ int64) (*educoder.HomeworkProgress, error) {
		reads.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	homeworks := make([]educoder.Homework, 10)
	for i := range homeworks {
		homeworks[i].ID = int64(i + 1)
	}
	done := make(chan []homeworkDTO, 1)
	go func() { done <- serviceHomeworks(ctx, fake, "COURSE1", homeworks) }()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("four bounded reads did not start")
		}
	}
	if reads.Load() != 4 {
		t.Fatalf("expected exactly four in-flight reads, got %d", reads.Load())
	}
	cancel()
	select {
	case result := <-done:
		if len(result) != 10 || reads.Load() != 4 {
			t.Fatalf("cancellation started additional reads: len=%d reads=%d", len(result), reads.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("canceled reads did not release handler")
	}
}

func TestHomeworkProgressEnrichmentHasBoundedAssignmentCount(t *testing.T) {
	var reads atomic.Int32
	fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
		reads.Add(1)
		return optionalListProgress(1, 1), nil
	}}
	homeworks := make([]educoder.Homework, 101)
	for i := range homeworks {
		homeworks[i] = educoder.Homework{ID: int64(i + 1), ChallengeCount: 1}
	}
	result := serviceHomeworks(context.Background(), fake, "COURSE1", homeworks)
	if reads.Load() != 100 || len(result) != 101 || result[100].RequiredKnown {
		t.Fatalf("enrichment bound lost: reads=%d len=%d last=%+v", reads.Load(), len(result), result[100])
	}
}
