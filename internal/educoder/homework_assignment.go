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

// canonicalHomeworkAssignment uses the complete homework-specific progress
// response and catalog count as the assignment proof. Header challenges can be
// only the code-reviewable subset (omitting required objective levels), so they
// may cross-check identities but must never select or exclude progress rows.
func canonicalHomeworkAssignment(hw Homework, progress *HomeworkProgress, h collectionHeader) (map[string]ChallengeProgress, error) {
	if _, _, valid := RequiredHomeworkCounts(hw, progress); !valid {
		return nil, errors.New("homework score roster incomplete or exceeds 100 levels")
	}
	headerIDs := map[int64]string{}
	headerNames := map[string]int64{}
	for _, ch := range h.Challenges {
		if ch.ID < 1 || strings.TrimSpace(ch.Name) == "" || headerIDs[ch.ID] != "" || headerNames[ch.Name] != 0 {
			return nil, errors.New("ambiguous homework header roster")
		}
		headerIDs[ch.ID] = ch.Name
		headerNames[ch.Name] = ch.ID
	}
	canonical := map[string]ChallengeProgress{}
	for _, ch := range progress.Challenges {
		if headerName, listed := headerIDs[ch.ChallengeID]; listed && headerName != ch.Name {
			return nil, errors.New("ambiguous or mismatched homework score roster")
		}
		if headerID, listed := headerNames[ch.Name]; listed && headerID != ch.ChallengeID {
			return nil, errors.New("ambiguous or mismatched homework score roster")
		}
		canonical[ch.Name] = ch
	}
	return canonical, nil
}
