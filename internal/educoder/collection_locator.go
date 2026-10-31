package educoder

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Evidence: the task bundle's service export g6 calls GET
// myshixuns/{identifier}/challenges.json with homework_common_id. Its caller
// passes task.myshixun.identifier, not task.game.identifier. Unlike shixun_exec,
// this endpoint only lists an existing runtime. Real student reads returned an
// array of {identifier,position,name,status,challenge_st}; no challenge ID.
type CollectionLocator struct {
	Game               string
	ChallengeID        int64
	MyShixunIdentifier string
	Position           int
	Name               string
}

type CollectionLocationResult struct {
	Targets []CollectionLocator
	Reasons map[string]int
}

type collectionHeader struct {
	HomeworkID int64  `json:"homework_id"`
	WorkID     int64  `json:"work_id"`
	MyShixun   string `json:"myshixun_identifier"`
	Shixun     string `json:"shixun_identifier"`
	Challenges []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"challenges"`
}
type collectionGame struct {
	Identifier string `json:"identifier"`
	Position   int    `json:"position"`
	Name       string `json:"name"`
	Status     int    `json:"status"`
	ST         *int   `json:"challenge_st"`
}

// CollectionLocate resolves only this account's listed homework/runtime. The
// header supplies canonical challenge IDs; positions/names join the runtime
// list to it, and exact full-score challenge evidence authorizes each target.
// A generic status=2 does not authorize a detail read by itself.
func (c *Client) CollectionLocate(ctx context.Context, course string, hw Homework, progress *HomeworkProgress) (CollectionLocationResult, error) {
	out := CollectionLocationResult{Reasons: map[string]int{}}
	if validIdentifier(course) != nil || hw.ID < 1 || validIdentifier(hw.ShixunIdentifier) != nil || progress == nil {
		return out, errors.New("invalid collection location context")
	}
	var response struct {
		Data collectionHeader `json:"data"`
	}
	ref := "/classrooms/" + course + "/shixun_homework/" + strconv.FormatInt(hw.ID, 10) + "/detail"
	if err := c.request(ctx, http.MethodGet, "/api/shixun_homeworks/"+strconv.FormatInt(hw.ID, 10)+"/header_info.json", nil, nil, ref, &response); err != nil {
		return out, err
	}
	h := response.Data
	if h.HomeworkID != hw.ID || h.Shixun != hw.ShixunIdentifier || validIdentifier(h.MyShixun) != nil || h.WorkID < 1 || (hw.StudentWorkID > 0 && hw.StudentWorkID != h.WorkID) || (hw.MyShixunIdentifier != "" && hw.MyShixunIdentifier != h.MyShixun) {
		out.Reasons["locator_homework_identity_mismatch"]++
		return out, nil
	}
	if len(h.Challenges) > 500 {
		out.Reasons["locator_challenge_roster_invalid"]++
		return out, nil
	}
	// Validate before any runtime request: malformed/duplicate canonical IDs or
	// names could otherwise make a passed row point to another challenge.
	ids := map[int64]bool{}
	names := map[string]bool{}
	for _, ch := range h.Challenges {
		if ch.ID < 1 || ch.Name == "" || ids[ch.ID] || names[ch.Name] {
			out.Reasons["locator_challenge_roster_ambiguous"]++
			return out, nil
		}
		ids[ch.ID] = true
		names[ch.Name] = true
	}
	full := map[int64]ChallengeProgress{}
	fullNames := map[string]ChallengeProgress{}
	progressNames := map[string]bool{}
	for _, p := range progress.Challenges {
		if p.Name == "" || progressNames[p.Name] {
			out.Reasons["locator_score_roster_ambiguous"]++
			return out, nil
		}
		progressNames[p.Name] = true
		if p.HasFullScore() {
			if full[p.ChallengeID].ChallengeID != 0 {
				out.Reasons["locator_score_roster_ambiguous"]++
				return out, nil
			}
			full[p.ChallengeID] = p
			fullNames[p.Name] = p
		}
	}
	var games []collectionGame
	q := url.Values{"homework_common_id": {strconv.FormatInt(hw.ID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/myshixuns/"+h.MyShixun+"/challenges.json", q, nil, ref, &games); err != nil {
		return out, err
	}
	if len(games) > 500 {
		return out, ErrCollectionLimit
	}
	seenGame := map[string]bool{}
	seenPosition := map[int]bool{}
	for _, g := range games {
		if validIdentifier(g.Identifier) != nil || g.Position < 1 || g.Position > 500 || (len(h.Challenges) > 0 && g.Position > len(h.Challenges)) || seenGame[g.Identifier] || seenPosition[g.Position] {
			out.Reasons["locator_game_roster_ambiguous"]++
			return out, nil
		}
		seenGame[g.Identifier] = true
		seenPosition[g.Position] = true
	}
	for _, g := range games {
		// Some real headers omit challenges entirely (notably choices).
		// The score response still binds an exact unique name to challenge_id.
		// Join only unique exact names, then verify ID, name and runtime position
		// again on the loaded task. Never equate a runtime identifier to a game.
		p, ok := fullNames[g.Name]
		if len(h.Challenges) > 0 {
			ch := h.Challenges[g.Position-1]
			p, ok = full[ch.ID]
			if g.Name != ch.Name {
				out.Reasons["locator_challenge_identity_or_pass_mismatch"]++
				continue
			}
		}
		if !ok {
			continue
		} // Never load a task without positive exact-full-score evidence.
		if g.Status != 2 || p.Name != g.Name || (p.ST != nil && g.ST != nil && *p.ST != *g.ST) {
			out.Reasons["locator_challenge_identity_or_pass_mismatch"]++
			continue
		}
		out.Targets = append(out.Targets, CollectionLocator{Game: g.Identifier, ChallengeID: p.ChallengeID, MyShixunIdentifier: h.MyShixun, Position: g.Position, Name: g.Name})
	}
	if len(out.Targets) == 0 && len(out.Reasons) == 0 {
		out.Reasons["locator_no_matching_full_score_game"]++
	}
	return out, nil
}
