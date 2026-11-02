package educoder

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// ResolveHomeworkLevel binds one planned required challenge to its executable
// game. Upstream creates games lazily: challenges.json returns status=3 and a
// null identifier until that challenge is entered. The original frontend's
// shixun entry service uses shixun_exec.json with challenge_id for this action.
// Collection deliberately never calls this method.
//
// The returned task is checked against every assignment/runtime/challenge
// identity in the plan. A skip restriction can return a different earlier game;
// access=false and mismatched tasks both stop before any solver can write.
func (c *Client) ResolveHomeworkLevel(ctx context.Context, r *HomeworkRoster, level HomeworkLevel) (HomeworkLevel, *Task, error) {
	if c == nil || r == nil || validIdentifier(r.Course) != nil || r.HomeworkID < 1 || validIdentifier(r.Shixun) != nil || validIdentifier(r.MyShixun) != nil {
		return level, nil, errors.New("invalid homework level resolution context")
	}
	matches := 0
	for _, planned := range r.Levels {
		if planned.ChallengeID == level.ChallengeID {
			if !sameHomeworkLevelPlan(planned, level) {
				return level, nil, errors.New("homework level differs from planned roster")
			}
			matches++
		}
	}
	if matches != 1 {
		return level, nil, errors.New("homework level is not unique in planned roster")
	}
	if level.Optional {
		return level, nil, ErrOptionalTask
	}
	if level.ChallengeID < 1 || level.Position < 1 || level.Position > 500 || level.Title == "" {
		return level, nil, errors.New("invalid planned homework level")
	}
	if level.RuntimePending {
		if level.Game != "" {
			return level, nil, errors.New("pending homework level already has a game")
		}
		q := url.Values{
			"id":                 {r.Shixun},
			"challenge_id":       {strconv.FormatInt(level.ChallengeID, 10)},
			"homework_common_id": {strconv.FormatInt(r.HomeworkID, 10)},
		}
		var entry struct {
			Game   string `json:"game_identifier"`
			Access *bool  `json:"access"`
		}
		ref := "/classrooms/" + r.Course + "/shixun_homework/" + strconv.FormatInt(r.HomeworkID, 10) + "/detail"
		if err := c.request(ctx, http.MethodGet, "/api/shixuns/"+r.Shixun+"/shixun_exec.json", q, nil, ref, &entry); err != nil {
			return level, nil, err
		}
		if entry.Access != nil && !*entry.Access {
			return level, nil, errors.New("homework challenge entry denied by upstream sequencing")
		}
		if validIdentifier(entry.Game) != nil {
			return level, nil, errors.New("homework challenge entry returned no valid game")
		}
		level.Game = entry.Game
		level.RuntimePending = false
	} else if validIdentifier(level.Game) != nil {
		return level, nil, errors.New("homework level runtime has not been resolved")
	}
	// Use the strict reader: it verifies source homework/course fields before
	// normalizing the local context, rather than filling a missing homework ID.
	task, err := c.CollectionLoadTask(ctx, r.Course, r.HomeworkID, level.Game)
	if err != nil {
		return level, nil, err
	}
	if err := r.ValidateTask(task, level); err != nil {
		return level, nil, err
	}
	return level, task, nil
}

func sameHomeworkLevelPlan(a, b HomeworkLevel) bool {
	if a.ChallengeID != b.ChallengeID || a.Game != b.Game || a.Title != b.Title || a.Position != b.Position || a.Optional != b.Optional || a.RuntimePending != b.RuntimePending || (a.ST == nil) != (b.ST == nil) {
		return false
	}
	return a.ST == nil || *a.ST == *b.ST
}
