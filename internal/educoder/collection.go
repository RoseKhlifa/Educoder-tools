package educoder

// The collection client deliberately exposes only previously observed read
// endpoints. In particular shixun_exec is NOT a read: it can create a runtime.
import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrCollectionLimit = errors.New("collection request or response budget exceeded")
var ErrCollectionReadOnly = errors.New("collection endpoint is not a supported read")

type CollectionBudget struct {
	Requests                  int
	ResponseBytes, TotalBytes int64
	Interval                  time.Duration
}

func DefaultCollectionBudget() CollectionBudget {
	return CollectionBudget{Requests: 1200, ResponseBytes: 2 << 20, TotalBytes: 64 << 20, Interval: 100 * time.Millisecond}
}

type collectionTransport struct {
	mu       sync.Mutex
	base     http.RoundTripper
	origin   string
	budget   CollectionBudget
	requests int
	bytes    int64
	last     time.Time
}

// CollectionReader shares only the authenticated cookie jar. It neither edits
// the original client's transport nor follows redirects to unreviewed URLs.
func (c *Client) CollectionReader(b CollectionBudget) (*Client, error) {
	if c.HTTP == nil || c.LoginName == "" || b.Requests < 1 || b.Requests > 1200 || b.ResponseBytes < 1 || b.ResponseBytes > 2<<20 || b.TotalBytes < 1 || b.TotalBytes > 64<<20 || b.Interval < 0 {
		return nil, errors.New("invalid collection budget or session")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid collection origin")
	}
	base := c.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	transport := &collectionTransport{base: base, origin: u.Scheme + "://" + u.Host, budget: b}
	return &Client{BaseURL: c.BaseURL, Origin: c.Origin, LoginName: c.LoginName, HTTP: &http.Client{Jar: c.HTTP.Jar, Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func collectionReadPath(p string) bool {
	s := strings.Split(strings.Trim(p, "/"), "/")
	if len(s) == 4 && s[0] == "api" {
		return s[1] == "users" && s[3] == "courses.json" || s[1] == "courses" && s[3] == "homework_commons.json" || s[1] == "homework_commons" && s[3] == "shixun_challenge_data.json" || s[1] == "tasks" && s[3] == "rep_content.json"
	}
	return len(s) == 3 && s[0] == "api" && s[1] == "tasks" && strings.HasSuffix(s[2], ".json")
}

func (t *collectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Scheme+"://"+req.URL.Host != t.origin || !collectionReadPath(req.URL.Path) {
		return nil, ErrCollectionReadOnly
	}
	// One reader serializes its reads. The application also limits collection
	// to one active account globally, independent of answer/model workers.
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if t.requests >= t.budget.Requests || t.bytes >= t.budget.TotalBytes {
		return nil, ErrCollectionLimit
	}
	if wait := t.budget.Interval - time.Since(t.last); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	t.requests++
	t.last = time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	limit := t.budget.ResponseBytes
	if remain := t.budget.TotalBytes - t.bytes; remain < limit {
		limit = remain
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	resp.Body.Close()
	t.bytes += int64(len(data))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrCollectionLimit
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

// Page APIs have an explicit bounded page, unlike the interactive aggregate
// APIs. They query all homework statuses and both active/ended classroom lists.
func (c *Client) CollectionCoursesPage(ctx context.Context, status string, page int) ([]Course, error) {
	if c.LoginName == "" || (status != "processing" && status != "end") || page < 1 || page > 5 {
		return nil, ErrCollectionLimit
	}
	var data struct {
		Courses []Course `json:"courses"`
	}
	q := url.Values{"category": {""}, "status": {status}, "page": {strconv.Itoa(page)}, "per_page": {"15"}, "sort_by": {"updated_at"}, "sort_direction": {"desc"}, "username": {c.LoginName}}
	err := c.request(ctx, http.MethodGet, "/api/users/"+url.PathEscape(c.LoginName)+"/courses.json", q, nil, "/users/"+c.LoginName+"/classrooms", &data)
	for i := range data.Courses {
		if data.Courses[i].Identifier == "" {
			data.Courses[i].Identifier = CourseIdentifier(data.Courses[i].FirstCategoryURL)
		}
	}
	return data.Courses, err
}

func (c *Client) CollectionHomeworksPage(ctx context.Context, course string, page int) ([]Homework, error) {
	if validIdentifier(course) != nil || page < 1 || page > 5 {
		return nil, ErrCollectionLimit
	}
	var data struct {
		Homeworks []Homework `json:"homeworks"`
	}
	q := url.Values{"id": {course}, "limit": {"20"}, "page": {strconv.Itoa(page)}, "status": {"0"}, "order": {"0"}}
	err := c.request(ctx, http.MethodGet, "/api/courses/"+course+"/homework_commons.json", q, nil, "/classrooms/"+course+"/shixun_homework", &data)
	return data.Homeworks, err
}

// Unlike the interactive loader, collection must not fill in a missing upstream
// homework identity from its request and then treat that value as evidence.
func (c *Client) CollectionLoadTask(ctx context.Context, course string, homeworkID int64, game string) (*Task, error) {
	if validIdentifier(course) != nil || validIdentifier(game) != nil || homeworkID < 1 {
		return nil, ErrCollectionReadOnly
	}
	var t Task
	err := c.request(ctx, http.MethodGet, "/api/tasks/"+game+".json", url.Values{"homework_common_id": {strconv.FormatInt(homeworkID, 10)}}, nil, "/tasks/"+course+"/"+strconv.FormatInt(homeworkID, 10)+"/"+game, &t)
	if err != nil {
		return nil, err
	}
	if t.Game.Identifier != game || t.HomeworkID != homeworkID || (t.CourseIdentifier != "" && t.CourseIdentifier != course) {
		return nil, errors.New("collection task identity missing or mismatched")
	}
	t.CourseIdentifier = course
	return &t, nil
}
