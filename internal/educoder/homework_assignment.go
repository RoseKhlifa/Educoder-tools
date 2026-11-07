package educoder

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// readHomeworkHeader only reads assignment metadata. Resolving a runtime or
// starting a game is deliberately not part of assignment accounting.
func (c *Client) readHomeworkHeader(ctx context.Context, course string, hw Homework) (collectionHeader, error) {
	if c == nil || c.HTTP == nil || validIdentifier(course) != nil || hw.ID < 1 || validIdentifier(hw.ShixunIdentifier) != nil || !hw.IsShixun {
		return collectionHeader{}, errors.New("invalid homework roster context")
	}
	ref := "/classrooms/" + course + "/shixun_homework/" + strconv.FormatInt(hw.ID, 10) + "/detail"
	var response struct {
		Data collectionHeader `json:"data"`
	}
	if err := c.readMetadata(ctx, metadataHeader, "/api/shixun_homeworks/"+strconv.FormatInt(hw.ID, 10)+"/header_info.json", nil, ref, &response, nil); err != nil {
		return collectionHeader{}, err
	}
	h := response.Data
	if h.HomeworkID != hw.ID || h.Shixun != hw.ShixunIdentifier || (hw.StudentWorkID > 0 && hw.StudentWorkID != h.WorkID) || (hw.MyShixunIdentifier != "" && h.MyShixun != hw.MyShixunIdentifier) {
		return collectionHeader{}, errors.New("homework header identity mismatch")
	}
	if h.MyShixun == "" {
		return collectionHeader{}, ErrHomeworkRuntimeMissing
	}
	if validIdentifier(h.MyShixun) != nil || h.WorkID < 1 {
		return collectionHeader{}, errors.New("homework runtime identity missing")
	}
	return h, nil
}

// canonicalHomeworkAssignment is shared by execution and read-only counts.
// Header IDs select assignment rows out of a possibly broader score response;
// exact names bind those rows to the runtime's name-based mapping. Upstream
// returns both broader headers with an exact assignment catalog count and
// assignment-only headers with a full-shixun catalog count. Either proof must
// hold, and every selected row must have an unambiguous identity.
func canonicalHomeworkAssignment(hw Homework, progress *HomeworkProgress, h collectionHeader) (map[string]ChallengeProgress, error) {
	if progress == nil {
		return nil, errors.New("homework score roster missing")
	}
	headerIDs := map[int64]string{}
	headerNames := map[string]bool{}
	for _, ch := range h.Challenges {
		if ch.ID < 1 || strings.TrimSpace(ch.Name) == "" || headerIDs[ch.ID] != "" || headerNames[ch.Name] {
			return nil, errors.New("ambiguous homework header roster")
		}
		headerIDs[ch.ID] = ch.Name
		headerNames[ch.Name] = true
	}
	canonical := map[string]ChallengeProgress{}
	ids := map[int64]bool{}
	for _, ch := range progress.Challenges {
		// Progress can include unassigned levels. They never enter either
		// accounting or execution, even when their scores are zero.
		if len(headerIDs) > 0 && headerIDs[ch.ChallengeID] == "" {
			continue
		}
		if ch.ChallengeID < 1 || strings.TrimSpace(ch.Name) == "" || ids[ch.ChallengeID] || canonical[ch.Name].ChallengeID != 0 || (len(headerIDs) > 0 && headerIDs[ch.ChallengeID] != ch.Name) {
			return nil, errors.New("ambiguous or mismatched homework score roster")
		}
		ids[ch.ChallengeID] = true
		canonical[ch.Name] = ch
	}
	catalogCountMatches := hw.ChallengeCount <= 0 || len(canonical) == hw.ChallengeCount
	headerProvesAssigned := len(headerIDs) > 0 && len(canonical) == len(headerIDs) && len(headerIDs) <= hw.ChallengeCount
	if len(canonical) < 1 || len(canonical) > 100 || (!catalogCountMatches && !headerProvesAssigned) {
		return nil, errors.New("homework score roster incomplete or exceeds 100 levels")
	}
	return canonical, nil
}
