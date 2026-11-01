package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"educoder-cli/internal/educoder"
)

// EducoderClient is the small part of the protocol client used by the web
// layer. Keeping this interface here lets handlers be tested without network
// access while the production implementation remains *educoder.Client.
type EducoderClient interface {
	Login(ctx context.Context, login, password string) (*educoder.User, error)
	UserInfo(ctx context.Context, course string) (*educoder.User, error)
	Courses(ctx context.Context, status string) ([]educoder.Course, error)
	Homeworks(ctx context.Context, course, category string) ([]educoder.Homework, error)
	Enter(ctx context.Context, course string, homeworkID int64, shixun string) (*educoder.Task, error)
}

var _ EducoderClient = (*educoder.Client)(nil)

type userDTO struct {
	UserID    int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	RealName  string `json:"realName,omitempty"`
	Identity  string `json:"identity"`
	AvatarURL string `json:"avatarUrl"`
}

func toUserDTO(user educoder.User) userDTO {
	return userDTO{UserID: user.UserID, Login: user.Login, Name: user.Name, RealName: user.RealName, Identity: user.Identity, AvatarURL: "/api/avatar"}
}

type courseDTO struct {
	ID            int64  `json:"id"`
	Identifier    string `json:"identifier"`
	Name          string `json:"name"`
	IsEnd         bool   `json:"ended"`
	HomeworkCount int    `json:"homeworkCount"`
}

func toCourseDTO(course educoder.Course) courseDTO {
	return courseDTO{ID: course.ID, Identifier: course.Identifier, Name: course.Name, IsEnd: course.IsEnd, HomeworkCount: course.HomeworkCount}
}

type homeworkDTO struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Status           []string `json:"status,omitempty"`
	ShixunIdentifier string   `json:"shixunIdentifier,omitempty"`
	IsShixun         bool     `json:"isShixun"`
	EnterStatus      int      `json:"enterStatus"`
	Category         string   `json:"category,omitempty"`
	ChallengeCount   int      `json:"challengeCount,omitempty"`
	FinishedCount    int      `json:"finishedChallengeCount,omitempty"`
	RequiredCount    *int     `json:"requiredChallengeCount,omitempty"`
	FinishedRequired *int     `json:"finishedRequiredChallengeCount,omitempty"`
	RequiredKnown    bool     `json:"requiredProgressKnown"`
	Deadline         string   `json:"deadline,omitempty"`
}

func toHomeworkDTO(homework educoder.Homework) homeworkDTO {
	return homeworkDTO{ID: homework.ID, Name: homework.Name, Status: homework.Status, ShixunIdentifier: homework.ShixunIdentifier, IsShixun: homework.IsShixun, EnterStatus: homework.EnterStatus, Category: homework.Category, ChallengeCount: homework.ChallengeCount, FinishedCount: homework.FinishedCount, Deadline: homeworkDeadline(homework)}
}

// homeworkDeadline accepts the string and numeric forms seen in the upstream
// classroom response. Numeric values are interpreted as Unix seconds or
// milliseconds and emitted as RFC3339 for the browser's local date formatter.
func homeworkDeadline(homework educoder.Homework) string {
	for _, value := range []any{homework.Deadline, homework.EndTime, homework.EndDate} {
		if result := normalizeDeadline(value); result != "" {
			return result
		}
	}
	return ""
}

func normalizeDeadline(value any) string {
	switch current := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(current)
	case json.Number:
		return normalizeDeadlineNumber(string(current))
	case float64:
		if math.IsNaN(current) || math.IsInf(current, 0) {
			return ""
		}
		return normalizeDeadlineNumber(strconv.FormatFloat(current, 'f', -1, 64))
	default:
		return strings.TrimSpace(fmt.Sprint(current))
	}
}

func normalizeDeadlineNumber(raw string) string {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return ""
	}
	if value > 100000000000 {
		value /= 1000
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

type taskDTO struct {
	Kind           string `json:"kind"`
	RequiredStatus string `json:"requiredStatus,omitempty"`
	GameCount      int    `json:"gameCount,omitempty"`
	Game           struct {
		ID         int64  `json:"id"`
		Identifier string `json:"identifier"`
		MyShixunID int64  `json:"myshixunId"`
		Status     int    `json:"status"`
	} `json:"game"`
	Challenge struct {
		ID          int64  `json:"id"`
		Subject     string `json:"subject"`
		Description string `json:"description,omitempty"`
		Path        string `json:"path,omitempty"`
		Position    int    `json:"position,omitempty"`
	} `json:"challenge"`
	Shixun struct {
		ID              int64  `json:"id"`
		Identifier      string `json:"identifier"`
		Name            string `json:"name"`
		ChallengesCount int    `json:"challengesCount,omitempty"`
	} `json:"shixun"`
	CourseIdentifier string  `json:"courseIdentifier"`
	HomeworkID       int64   `json:"homeworkId"`
	NextGame         *string `json:"nextGame,omitempty"`
	URL              string  `json:"url,omitempty"`
}

func toTaskDTO(task educoder.Task) taskDTO {
	var result taskDTO
	result.Kind = string(task.Kind())
	result.RequiredStatus = task.RequiredStatus
	result.GameCount = task.GameCount
	result.Game.ID = task.Game.ID
	result.Game.Identifier = task.Game.Identifier
	result.Game.MyShixunID = task.Game.MyShixunID
	result.Game.Status = task.Game.Status
	result.Challenge.ID = task.Challenge.ID
	result.Challenge.Subject = task.Challenge.Subject
	result.Challenge.Description = task.Challenge.Description
	result.Challenge.Path = task.Challenge.Path
	result.Challenge.Position = task.Challenge.Position
	result.Shixun.ID = task.Shixun.ID
	result.Shixun.Identifier = task.Shixun.Identifier
	result.Shixun.Name = task.Shixun.Name
	result.Shixun.ChallengesCount = task.Shixun.ChallengesCount
	result.CourseIdentifier = task.CourseIdentifier
	result.HomeworkID = task.HomeworkID
	result.NextGame = task.NextGame
	result.URL = task.URL
	return result
}
