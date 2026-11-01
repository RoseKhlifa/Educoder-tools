package web

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/platform"
)

type adminJobResponse struct {
	jobs.AdminRecord
	Context AdminJobContext `json:"context"`
}

// Join verified platform identities and display labels on the server. The
// user's first page of accounts is never used as an ownership/name lookup.
// Result labels can also explain historical records without network requests.
func (s *Server) adminJobResponses(ctx context.Context, records []jobs.AdminRecord) ([]adminJobResponse, error) {
	out := make([]adminJobResponse, len(records))
	courses, homeworks := map[string]string{}, map[string]string{}
	homeworkKey := func(course string, id int64) string { return course + ":" + strconv.FormatInt(id, 10) }
	remember := func(course, courseName string, id int64, homeworkName string) {
		if course != "" && courseName != "" && courses[course] == "" {
			courses[course] = courseName
		}
		key := homeworkKey(course, id)
		if course != "" && id > 0 && homeworkName != "" && homeworks[key] == "" {
			homeworks[key] = homeworkName
		}
	}
	for i, record := range records {
		c := deriveJobContext(record.Job)
		out[i] = adminJobResponse{AdminRecord: record, Context: c}
		remember(c.Course.Identifier, c.Course.Name, c.Homework.ID, c.Homework.Name)
		for _, h := range c.Summary.AttentionHomeworks {
			remember(h.CourseIdentifier, h.CourseName, h.ID, h.Name)
		}
	}
	type labelPair struct{ course, homework string }
	labels := map[string]labelPair{}
	users := map[string]*AdminJobContextUser{}
	fill := func(course *AdminJobCourse, homework *AdminJobHomework) error {
		if course.Identifier == "" {
			return nil
		}
		key := homeworkKey(course.Identifier, homework.ID)
		if course.Name == "" {
			course.Name = courses[course.Identifier]
		}
		if homework.Name == "" {
			homework.Name = homeworks[key]
		}
		if s.platform == nil || (course.Name != "" && (homework.ID == 0 || homework.Name != "")) {
			return nil
		}
		pair, found := labels[key]
		if !found {
			var err error
			pair.course, pair.homework, err = s.platform.LookupJobLabels(ctx, course.Identifier, homework.ID)
			if err != nil && !errors.Is(err, platform.ErrInvalidJobLabel) {
				return err
			}
			labels[key] = pair
		}
		if course.Name == "" {
			course.Name = pair.course
		}
		if homework.Name == "" {
			homework.Name = pair.homework
		}
		return nil
	}
	for i := range out {
		response := &out[i]
		owner := response.OwnerID
		user, found := users[owner]
		if !found {
			user = &AdminJobContextUser{Name: "历史账号未关联"}
			if id, ok := strings.CutPrefix(owner, "platform:"); ok && s.platform != nil {
				u, err := s.platform.LookupUser(ctx, id)
				if err != nil && !errors.Is(err, platform.ErrNotFound) {
					return nil, err
				}
				if err == nil {
					name := u.Name
					if name == "" {
						name = u.Login
					}
					user = &AdminJobContextUser{ID: u.ID, Name: name, Login: u.Login, Linked: true}
				} else {
					user = &AdminJobContextUser{ID: id, Name: "账号资料缺失"}
				}
			}
			users[owner] = user
		}
		response.Context.User = user
		if err := fill(&response.Context.Course, &response.Context.Homework); err != nil {
			return nil, err
		}
		for n := range response.Context.Summary.AttentionHomeworks {
			h := &response.Context.Summary.AttentionHomeworks[n]
			c, hw := AdminJobCourse{Identifier: h.CourseIdentifier, Name: h.CourseName}, AdminJobHomework{ID: h.ID, Name: h.Name}
			if err := fill(&c, &hw); err != nil {
				return nil, err
			}
			h.CourseName, h.Name = c.Name, hw.Name
		}
	}
	return out, nil
}

func (s *Server) rememberCourseLabels(ctx context.Context, courses []educoder.Course) {
	if s.platform == nil {
		return
	}
	labels := make([]platform.CourseLabel, 0, min(len(courses), 1000))
	for _, c := range courses {
		if c.Identifier != "" && c.Name != "" && len(labels) < 1000 {
			labels = append(labels, platform.CourseLabel{Identifier: c.Identifier, Name: c.Name})
		}
	}
	_ = s.platform.RememberCourses(ctx, labels)
}

func (s *Server) rememberHomeworkLabels(ctx context.Context, course string, homeworks []educoder.Homework) {
	if s.platform == nil {
		return
	}
	labels := make([]platform.HomeworkLabel, 0, min(len(homeworks), 1000))
	for _, h := range homeworks {
		if h.ID > 0 && h.Name != "" && len(labels) < 1000 {
			labels = append(labels, platform.HomeworkLabel{ID: h.ID, Name: h.Name})
		}
	}
	_ = s.platform.RememberHomeworks(ctx, course, labels)
}

// Backfill only label fields from durable results. This never reads credentials
// or contacts an upstream account, and does not alter any historical outcome.
func (s *Server) rememberResultLabels(ctx context.Context, request jobs.Request, raw json.RawMessage, historical bool) {
	if s.platform == nil || len(raw) == 0 || len(raw) > 4*1024*1024 {
		return
	}
	var result adminContextResult
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	courses := []platform.CourseLabel{}
	homeworks := map[string][]platform.HomeworkLabel{}
	course := request.Course
	if course == "" {
		course = result.Course
	}
	if course != "" && result.CourseName != "" {
		courses = append(courses, platform.CourseLabel{Identifier: course, Name: result.CourseName})
	}
	id := request.HomeworkID
	if id == 0 {
		id = result.HomeworkID
	}
	if course != "" && id > 0 && result.HomeworkName != "" {
		homeworks[course] = append(homeworks[course], platform.HomeworkLabel{ID: id, Name: result.HomeworkName})
	}
	for i, h := range result.Homeworks {
		if i >= 100 {
			break
		}
		if h.Course == "" {
			continue
		}
		if h.CourseName != "" {
			courses = append(courses, platform.CourseLabel{Identifier: h.Course, Name: h.CourseName})
		}
		if h.HomeworkID > 0 && h.Title != "" {
			homeworks[h.Course] = append(homeworks[h.Course], platform.HomeworkLabel{ID: h.HomeworkID, Name: h.Title})
		}
	}
	if historical {
		_ = s.platform.RememberMissingCourses(ctx, courses)
	} else {
		_ = s.platform.RememberCourses(ctx, courses)
	}
	for identifier, labels := range homeworks {
		if historical {
			_ = s.platform.RememberMissingHomeworks(ctx, identifier, labels)
		} else {
			_ = s.platform.RememberHomeworks(ctx, identifier, labels)
		}
	}
}
