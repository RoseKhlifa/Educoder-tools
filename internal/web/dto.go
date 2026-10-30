package web

import (
	"context"

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
	UserID   int64  `json:"id"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	RealName string `json:"realName,omitempty"`
	Identity string `json:"identity"`
}

func toUserDTO(user educoder.User) userDTO {
	return userDTO{UserID: user.UserID, Login: user.Login, Name: user.Name, RealName: user.RealName, Identity: user.Identity}
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
}

func toHomeworkDTO(homework educoder.Homework) homeworkDTO {
	return homeworkDTO{ID: homework.ID, Name: homework.Name, Status: homework.Status, ShixunIdentifier: homework.ShixunIdentifier, IsShixun: homework.IsShixun, EnterStatus: homework.EnterStatus, Category: homework.Category}
}

type taskDTO struct {
	RequiredStatus string `json:"requiredStatus,omitempty"`
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
	} `json:"challenge"`
	Shixun struct {
		ID         int64  `json:"id"`
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
	} `json:"shixun"`
	CourseIdentifier string  `json:"courseIdentifier"`
	HomeworkID       int64   `json:"homeworkId"`
	NextGame         *string `json:"nextGame,omitempty"`
	URL              string  `json:"url,omitempty"`
}

func toTaskDTO(task educoder.Task) taskDTO {
	var result taskDTO
	result.RequiredStatus = task.RequiredStatus
	result.Game.ID = task.Game.ID
	result.Game.Identifier = task.Game.Identifier
	result.Game.MyShixunID = task.Game.MyShixunID
	result.Game.Status = task.Game.Status
	result.Challenge.ID = task.Challenge.ID
	result.Challenge.Subject = task.Challenge.Subject
	result.Challenge.Description = task.Challenge.Description
	result.Challenge.Path = task.Challenge.Path
	result.Shixun.ID = task.Shixun.ID
	result.Shixun.Identifier = task.Shixun.Identifier
	result.Shixun.Name = task.Shixun.Name
	result.CourseIdentifier = task.CourseIdentifier
	result.HomeworkID = task.HomeworkID
	result.NextGame = task.NextGame
	result.URL = task.URL
	return result
}
