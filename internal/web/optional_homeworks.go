package web

import (
	"context"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

type homeworkProgressReader interface {
	HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error)
}

// serviceHomeworks reads progress only when the user opens one classroom's
// details. The classroom overview stays a single list request. Unknown or
// partial progress must not turn the upstream total (which includes optional
// work) into a claimed required-work count.
func serviceHomeworks(ctx context.Context, client EducoderClient, course string, homeworks []educoder.Homework) []homeworkDTO {
	result := make([]homeworkDTO, len(homeworks))
	for i, homework := range homeworks {
		result[i] = toHomeworkDTO(homework)
	}
	reader, ok := client.(homeworkProgressReader)
	if !ok {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	limit := min(len(homeworks), 100)
	indexes := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < min(limit, 4); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indexes {
				if ctx.Err() != nil {
					continue
				}
				progress, err := reader.HomeworkProgress(ctx, course, homeworks[index].ID)
				if err == nil {
					applyRequiredProgress(&result[index], homeworks[index], progress)
				}
			}
		}()
	}
send:
	for i := 0; i < limit; i++ {
		select {
		case <-ctx.Done():
			break send
		case indexes <- i:
		}
	}
	close(indexes)
	workers.Wait()
	visible := result[:0]
	for _, homework := range result {
		if homework.RequiredKnown && homework.RequiredCount != nil && *homework.RequiredCount == 0 {
			continue
		}
		visible = append(visible, homework)
	}
	return visible
}

func applyRequiredProgress(dto *homeworkDTO, homework educoder.Homework, progress *educoder.HomeworkProgress) {
	if progress == nil || homework.ChallengeCount < 1 || homework.ChallengeCount > 100 || len(progress.Challenges) != homework.ChallengeCount {
		return
	}
	ids, names := map[int64]bool{}, map[string]bool{}
	required, completed := 0, 0
	for _, challenge := range progress.Challenges {
		name := strings.TrimSpace(challenge.Name)
		if challenge.ChallengeID <= 0 || name == "" || ids[challenge.ChallengeID] || names[name] {
			return
		}
		ids[challenge.ChallengeID], names[name] = true, true
		if challenge.IsOptional() {
			continue
		}
		required++
		if challenge.HasFullScore() {
			completed++
		}
	}
	dto.RequiredKnown = true
	dto.RequiredCount, dto.FinishedRequired = &required, &completed
}
