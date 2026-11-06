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
	"strings"
)

var ErrHomeworkRuntimeMissing = errors.New("homework has no existing runtime")
var ErrHomeworkTaskIdentity = errors.New("homework task identity mismatches planned roster")
var ErrHomeworkTaskKind = errors.New("homework task question kind is unsupported or mismatched")

// HomeworkRosterPartialError isolates runtime rows that cannot be uniquely
// bound. The returned roster retains them for accounting, but never executes
// or picks one of their conflicting game identities.
type HomeworkRosterPartialError struct{ Diagnostics []string }

func (e *HomeworkRosterPartialError) Error() string { return strings.Join(e.Diagnostics, "; ") }

// HomeworkLevel belongs to the selected assignment, rather than every level
// in the underlying shixun. Position is the runtime's actual position and may
// therefore start above one or have gaps.
type HomeworkLevel struct {
	ChallengeID int64
	Game, Title string
	Position    int
	ST          *int
	Optional    bool
	// A status=3 runtime row has a canonical challenge and position but no
	// game yet. Resolve it immediately before execution, never in collection.
	RuntimePending bool
	RosterIssue    string
}

type HomeworkRoster struct {
	Course           string
	HomeworkID       int64
	Shixun, MyShixun string
	MyShixunID       int64
	Levels           []HomeworkLevel
}

// RequiredLevels is the execution roster. Optional levels remain in Levels
// for identity checks and historical views, but never enter a solver plan.
func (r *HomeworkRoster) RequiredLevels() []HomeworkLevel {
	if r == nil {
		return nil
	}
	levels := make([]HomeworkLevel, 0, len(r.Levels))
	for _, level := range r.Levels {
		if !level.Optional && level.RosterIssue == "" {
			levels = append(levels, level)
		}
	}
	return levels
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
	if err := c.readMetadata(ctx, metadataHeader, "/api/shixun_homeworks/"+strconv.FormatInt(hw.ID, 10)+"/header_info.json", nil, ref, &response, nil); err != nil {
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
	// Upstream exposes both roster shapes: a broader header with an exact
	// assignment count, or an assignment-only header while challenge_count
	// still counts the full shixun. Keep the count guard for the first shape.
	// Accept the second only when progress covers every unique header ID with
	// its exact name and the assigned roster fits within the catalog total.
	catalogCountMatches := hw.ChallengeCount <= 0 || len(canonical) == hw.ChallengeCount
	headerProvesAssigned := len(headerIDs) > 0 && len(canonical) == len(headerIDs) && len(headerIDs) <= hw.ChallengeCount
	if len(canonical) < 1 || len(canonical) > 100 || (!catalogCountMatches && !headerProvesAssigned) {
		return nil, errors.New("homework score roster incomplete or exceeds 100 levels")
	}
	var games []collectionGame
	q := url.Values{"homework_common_id": {strconv.FormatInt(hw.ID, 10)}}
	if err := c.readMetadata(ctx, metadataRoster, "/api/myshixuns/"+h.MyShixun+"/challenges.json", q, ref, &games, func() bool { return games != nil }); err != nil {
		return nil, err
	}
	if len(games) > 500 {
		return nil, errors.New("runtime roster exceeds limit")
	}
	r := &HomeworkRoster{Course: course, HomeworkID: hw.ID, Shixun: h.Shixun, MyShixun: h.MyShixun}
	// Count first, then select. Reject every participant in a collision instead
	// of keeping whichever ambiguous row happened to arrive first.
	gameCounts, positionCounts, selectedCounts := map[string]int{}, map[int]int{}, map[int64]int{}
	for _, g := range games {
		ch, selected := canonical[g.Name]
		if !selected || ch.IsOptional() {
			continue
		}
		selectedCounts[ch.ChallengeID]++
		positionCounts[g.Position]++
		if g.Identifier != "" {
			gameCounts[g.Identifier]++
		}
	}
	seenSelected := map[int64]bool{}
	issues := map[int64]string{}
	partial := &HomeworkRosterPartialError{}
	for i, g := range games {
		ch, selected := canonical[g.Name]
		// Runtime lists may include other assignment levels and elective rows.
		// Neither needs an executable game for this required-only plan.
		if !selected || ch.IsOptional() {
			continue
		}
		row := i + 1
		issue := ""
		switch {
		case g.Position < 1 || g.Position > 500:
			issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=position reason=out_of_range", row)
		case positionCounts[g.Position] > 1:
			issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=position reason=duplicate", row)
		case selectedCounts[ch.ChallengeID] > 1:
			issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=name reason=duplicate_selected_challenge", row)
		case ch.ST != nil && g.ST != nil && *ch.ST != *g.ST:
			issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=challenge_st reason=mismatch", row)
		}
		pending := g.Identifier == "" && g.Status == 3
		if issue == "" && !pending {
			if validIdentifier(g.Identifier) != nil {
				issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=identifier reason=invalid_initialized_game", row)
			}
			if gameCounts[g.Identifier] > 1 {
				issue = fmt.Sprintf("ambiguous runtime game roster: row=%d field=identifier reason=duplicate", row)
			}
		}
		if issue != "" {
			issues[ch.ChallengeID] = issue
			partial.Diagnostics = append(partial.Diagnostics, issue)
			continue
		}
		seenSelected[ch.ChallengeID] = true
		r.Levels = append(r.Levels, HomeworkLevel{ChallengeID: ch.ChallengeID, Game: g.Identifier, Title: g.Name, Position: g.Position, ST: ch.ST, RuntimePending: pending})
	}
	for _, ch := range canonical {
		if seenSelected[ch.ChallengeID] {
			continue
		}
		// An elective level does not need an executable runtime game. Its
		// canonical identity is retained, while every required level must map.
		if !ch.IsOptional() {
			issue := issues[ch.ChallengeID]
			if issue == "" {
				issue = "homework runtime mapping is incomplete"
				partial.Diagnostics = append(partial.Diagnostics, issue)
			}
			r.Levels = append(r.Levels, HomeworkLevel{ChallengeID: ch.ChallengeID, Title: ch.Name, ST: ch.ST, RosterIssue: issue})
			continue
		}
		r.Levels = append(r.Levels, HomeworkLevel{ChallengeID: ch.ChallengeID, Title: ch.Name, ST: ch.ST, Optional: true})
	}
	sort.Slice(r.Levels, func(i, j int) bool {
		if r.Levels[i].Position == r.Levels[j].Position {
			return r.Levels[i].ChallengeID < r.Levels[j].ChallengeID
		}
		if r.Levels[i].Position == 0 {
			return false
		}
		if r.Levels[j].Position == 0 {
			return true
		}
		return r.Levels[i].Position < r.Levels[j].Position
	})
	if len(partial.Diagnostics) > 0 {
		return r, partial
	}
	return r, nil
}

func (r *HomeworkRoster) ValidateTask(t *Task, level HomeworkLevel) error {
	if level.RosterIssue != "" || level.RuntimePending || validIdentifier(level.Game) != nil {
		return errors.New("homework level runtime has not been resolved")
	}
	if r == nil || t == nil || t.HomeworkID != r.HomeworkID || (t.CourseIdentifier != "" && t.CourseIdentifier != r.Course) || t.Game.Identifier != level.Game || t.Game.ID < 1 || t.Challenge.ID != level.ChallengeID || t.Challenge.Subject != level.Title || t.Challenge.Position != level.Position || t.Shixun.Identifier != r.Shixun || t.MyShixun.Identifier != r.MyShixun || t.MyShixun.ID < 1 || t.Game.MyShixunID != t.MyShixun.ID || (r.MyShixunID > 0 && t.MyShixun.ID != r.MyShixunID) {
		return ErrHomeworkTaskIdentity
	}
	if level.Optional || t.IsOptional() {
		return ErrOptionalTask
	}
	if t.Kind() == TaskKindUnknown || (level.ST != nil && (t.Challenge.ST == nil || *t.Challenge.ST != *level.ST)) {
		return ErrHomeworkTaskKind
	}
	return nil
}

// BindHomeworkLevel creates a per-execution client sharing the cookie jar and
// connection pool, without changing the user's client. Every task reload done
// by either solver is checked against the preflight identity before it can be
// used for generation or a write. This also guards mid-run upstream changes.
func (c *Client) BindHomeworkLevel(r *HomeworkRoster, level HomeworkLevel) (*Client, error) {
	if c == nil || c.HTTP == nil || r == nil {
		return nil, errors.New("missing homework bound client context")
	}
	if level.Optional {
		return nil, ErrOptionalTask
	}
	if level.RosterIssue != "" || level.RuntimePending || validIdentifier(level.Game) != nil {
		return nil, errors.New("homework level runtime has not been resolved")
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
