package educoder

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
// complete assignment-specific progress supplies canonical challenge IDs and
// names; the header binds the runtime but can omit objective levels. Exact
// full-score challenge evidence and a unique runtime name authorize each target.
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
	// A catalog count equal to the assignment-specific score roster proves
	// coverage. A code-only header must never shrink that roster. Collection
	// remains exhaustive even for legacy assignments above execution's cap.
	if hw.ChallengeCount < 1 || len(progress.Challenges) != hw.ChallengeCount {
		out.Reasons["locator_score_roster_incomplete"]++
		return out, nil
	}
	progressIDs := map[int64]ChallengeProgress{}
	progressNames := map[string]ChallengeProgress{}
	fullNames := map[string]ChallengeProgress{}
	for _, p := range progress.Challenges {
		name := strings.TrimSpace(p.Name)
		if p.ChallengeID < 1 || name == "" || progressIDs[p.ChallengeID].ChallengeID != 0 || progressNames[name].ChallengeID != 0 {
			out.Reasons["locator_score_roster_ambiguous"]++
			return out, nil
		}
		progressIDs[p.ChallengeID] = p
		progressNames[name] = p
		if p.HasFullScore() {
			fullNames[p.Name] = p
		}
	}
	// Cross-check overlapping IDs and names, not positions or header coverage.
	// A genuine catalog/progress subset may have a broader runtime/header.
	headerIDs, headerNames := map[int64]bool{}, map[string]bool{}
	for _, ch := range h.Challenges {
		name := strings.TrimSpace(ch.Name)
		if ch.ID < 1 || name == "" || headerIDs[ch.ID] || headerNames[name] {
			out.Reasons["locator_challenge_roster_ambiguous"]++
			return out, nil
		}
		headerIDs[ch.ID], headerNames[name] = true, true
		if p, ok := progressIDs[ch.ID]; ok && p.Name != ch.Name {
			out.Reasons["locator_challenge_identity_or_pass_mismatch"]++
			return out, nil
		}
		if p, ok := progressNames[name]; ok && (p.ChallengeID != ch.ID || p.Name != ch.Name) {
			out.Reasons["locator_challenge_identity_or_pass_mismatch"]++
			return out, nil
		}
	}
	var games []collectionGame
	q := url.Values{"homework_common_id": {strconv.FormatInt(hw.ID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/myshixuns/"+h.MyShixun+"/challenges.json", q, nil, ref, &games); err != nil {
		return out, err
	}
	seenGame := map[string]bool{}
	seenPosition := map[int]bool{}
	seenSelected := map[int64]bool{}
	initializedGames := make([]collectionGame, 0, len(games))
	for _, g := range games {
		if g.Position < 1 || seenPosition[g.Position] {
			out.Reasons["locator_game_roster_ambiguous"]++
			return out, nil
		}
		seenPosition[g.Position] = true
		if p, selected := progressNames[g.Name]; selected {
			if seenSelected[p.ChallengeID] {
				out.Reasons["locator_game_roster_ambiguous"]++
				return out, nil
			}
			seenSelected[p.ChallengeID] = true
		}
		// A not-yet-entered challenge has no game to collect. It must not
		// prevent collection of other, positively verified full-score rows.
		// Never initialize such a game from the read-only collector.
		if g.Identifier == "" && g.Status == 3 && fullNames[g.Name].ChallengeID == 0 {
			continue
		}
		if validIdentifier(g.Identifier) != nil || seenGame[g.Identifier] {
			out.Reasons["locator_game_roster_ambiguous"]++
			return out, nil
		}
		seenGame[g.Identifier] = true
		initializedGames = append(initializedGames, g)
	}
	for _, g := range initializedGames {
		// The score response binds an exact unique name to challenge_id, even
		// when the header omits some or all of the objective challenges.
		// Join only unique exact names, then verify ID, name and runtime position
		// again on the loaded task. Never equate a runtime identifier to a game.
		p, ok := fullNames[g.Name]
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
