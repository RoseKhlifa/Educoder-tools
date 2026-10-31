package educoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

var ErrHomeworkRuntimeMissing = errors.New("homework has no existing runtime")

// HomeworkLevel belongs to the selected assignment, rather than every level
// in the underlying shixun. Position is the runtime's actual position and may
// therefore start above one or have gaps.
type HomeworkLevel struct {
	ChallengeID int64
	Game, Title string
	Position    int
	ST          *int
}

type HomeworkRoster struct {
	Course           string
	HomeworkID       int64
	Shixun, MyShixun string
	MyShixunID       int64
	Levels           []HomeworkLevel
}

// HomeworkRoster joins three observed read endpoints. The homework progress
// response supplies the assignment's canonical IDs, the header binds its
// student runtime, and the runtime list supplies executable game identifiers.
// A name must match uniquely; runtime positions never index an assignment-only
// slice because the assignment can select a subset of the shixun.
func (c *Client) HomeworkRoster(ctx context.Context, course string, hw Homework) (*HomeworkRoster, error) {
	if validIdentifier(course) != nil || hw.ID < 1 || validIdentifier(hw.ShixunIdentifier) != nil || !hw.IsShixun {
		return nil, errors.New("invalid homework roster context")
	}
	ref := "/classrooms/" + course + "/shixun_homework/" + strconv.FormatInt(hw.ID, 10) + "/detail"
	var response struct {
		Data collectionHeader `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/shixun_homeworks/"+strconv.FormatInt(hw.ID, 10)+"/header_info.json", nil, nil, ref, &response); err != nil {
		return nil, err
	}
	h := response.Data
	if h.HomeworkID != hw.ID || h.Shixun != hw.ShixunIdentifier || (hw.StudentWorkID > 0 && hw.StudentWorkID != h.WorkID) || (hw.MyShixunIdentifier != "" && h.MyShixun != hw.MyShixunIdentifier) {
		return nil, errors.New("homework header identity mismatch")
	}
	if h.MyShixun == "" {
		return nil, ErrHomeworkRuntimeMissing
	}
	if validIdentifier(h.MyShixun) != nil || h.WorkID < 1 {
		return nil, errors.New("homework runtime identity missing")
	}
	p, err := c.HomeworkProgress(ctx, course, hw.ID)
	if err != nil {
		return nil, err
	}
	headerIDs := map[int64]string{}
	headerNames := map[string]bool{}
	for _, ch := range h.Challenges {
		if ch.ID < 1 || ch.Name == "" || headerIDs[ch.ID] != "" || headerNames[ch.Name] {
			return nil, errors.New("ambiguous homework header roster")
		}
		headerIDs[ch.ID] = ch.Name
		headerNames[ch.Name] = true
	}
	canonical := map[string]ChallengeProgress{}
	ids := map[int64]bool{}
	for _, ch := range p.Challenges {
		// Progress can contain levels outside this homework's assignment. When
		// header_info supplies the canonical assignment IDs, ignore those extras
		// while still rejecting malformed/duplicate selected entries.
		if len(headerIDs) > 0 && headerIDs[ch.ChallengeID] == "" {
			continue
		}
		if ch.ChallengeID < 1 || ch.Name == "" || ids[ch.ChallengeID] || canonical[ch.Name].ChallengeID != 0 || (len(headerIDs) > 0 && headerIDs[ch.ChallengeID] != ch.Name) {
			return nil, errors.New("ambiguous or mismatched homework score roster")
		}
		ids[ch.ChallengeID] = true
		canonical[ch.Name] = ch
	}
	// header_info can describe every challenge in the underlying shixun,
	// while the selected homework may contain only a subset. The assignment's
	// progress list and challenge_count are the authoritative selected roster;
	// header IDs are used only to reject unknown progress entries and to verify
	// names for the selected IDs.
	if len(canonical) < 1 || len(canonical) > 100 || (hw.ChallengeCount > 0 && len(canonical) != hw.ChallengeCount) {
		return nil, errors.New("homework score roster incomplete or exceeds 100 levels")
	}
	var games []collectionGame
	q := url.Values{"homework_common_id": {strconv.FormatInt(hw.ID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/myshixuns/"+h.MyShixun+"/challenges.json", q, nil, ref, &games); err != nil {
		return nil, err
	}
	if len(games) > 500 {
		return nil, errors.New("runtime roster exceeds limit")
	}
	r := &HomeworkRoster{Course: course, HomeworkID: hw.ID, Shixun: h.Shixun, MyShixun: h.MyShixun}
	seenGame := map[string]bool{}
	seenPosition := map[int]bool{}
	seenSelected := map[int64]bool{}
	for _, g := range games {
		if validIdentifier(g.Identifier) != nil || g.Position < 1 || g.Position > 500 || seenGame[g.Identifier] || seenPosition[g.Position] {
			return nil, errors.New("ambiguous runtime game roster")
		}
		seenGame[g.Identifier] = true
		seenPosition[g.Position] = true
		ch, selected := canonical[g.Name]
		if !selected {
			continue
		}
		if seenSelected[ch.ChallengeID] || (ch.ST != nil && g.ST != nil && *ch.ST != *g.ST) {
			return nil, errors.New("ambiguous selected homework game")
		}
		seenSelected[ch.ChallengeID] = true
		r.Levels = append(r.Levels, HomeworkLevel{ChallengeID: ch.ChallengeID, Game: g.Identifier, Title: g.Name, Position: g.Position, ST: ch.ST})
	}
	if len(r.Levels) != len(canonical) {
		return nil, errors.New("homework runtime mapping is incomplete")
	}
	sort.Slice(r.Levels, func(i, j int) bool { return r.Levels[i].Position < r.Levels[j].Position })
	return r, nil
}

func (r *HomeworkRoster) ValidateTask(t *Task, level HomeworkLevel) error {
	if t == nil || t.HomeworkID != r.HomeworkID || (t.CourseIdentifier != "" && t.CourseIdentifier != r.Course) || t.Game.Identifier != level.Game || t.Game.ID < 1 || t.Challenge.ID != level.ChallengeID || t.Challenge.Subject != level.Title || t.Challenge.Position != level.Position || t.Shixun.Identifier != r.Shixun || t.MyShixun.Identifier != r.MyShixun || t.MyShixun.ID < 1 || t.Game.MyShixunID != t.MyShixun.ID || (r.MyShixunID > 0 && t.MyShixun.ID != r.MyShixunID) || t.Kind() == TaskKindUnknown || (level.ST != nil && (t.Challenge.ST == nil || *t.Challenge.ST != *level.ST)) {
		return errors.New("homework task identity or question kind mismatches planned roster")
	}
	return nil
}

// BindHomeworkLevel creates a per-execution client sharing the cookie jar and
// connection pool, without changing the user's client. Every task reload done
// by either solver is checked against the preflight identity before it can be
// used for generation or a write. This also guards mid-run upstream changes.
func (c *Client) BindHomeworkLevel(r *HomeworkRoster, level HomeworkLevel) (*Client, error) {
	if c.HTTP == nil || r == nil {
		return nil, errors.New("missing homework bound client context")
	}
	clone := *c
	httpClone := *c.HTTP
	base := httpClone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClone.Transport = &homeworkLevelTransport{base: base, roster: *r, level: level}
	clone.HTTP = &httpClone
	return &clone, nil
}

type homeworkLevelTransport struct {
	base   http.RoundTripper
	roster HomeworkRoster
	level  HomeworkLevel
}

func (t *homeworkLevelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if req.Method != http.MethodGet || req.URL.Path != "/api/tasks/"+t.level.Game+".json" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("homework task response exceeds limit")
	}
	var task Task
	if err = json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	if req.URL.Query().Get("homework_common_id") != strconv.FormatInt(t.roster.HomeworkID, 10) {
		return nil, errors.New("homework request scope mismatch")
	}
	if err = t.roster.ValidateTask(&task, t.level); err != nil {
		return nil, fmt.Errorf("task roster guard: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}
