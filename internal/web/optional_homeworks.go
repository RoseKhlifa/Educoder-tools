package web

import (
	"context"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

type homeworkProgressReader interface {
	HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error)
}

// serviceHomeworks enriches only executable shixun rows with required-level
// progress. Common text/image assignments have a different submission protocol
// and must remain visible without shixun progress requests or optional-level
// filtering. Unknown or partial progress cannot claim required-work completion.
func serviceHomeworks(ctx context.Context, client EducoderClient, course string, homeworks []educoder.Homework) []homeworkDTO {
	return serviceHomeworksSettled(ctx, client, course, homeworks, nil, "")
}

// serviceHomeworksSettled is serviceHomeworks with a per-account memory of
// assignments whose last complete progress read left nothing to do. Those skip
// their upstream progress read until the entry expires; every other assignment
// is read exactly as before. A nil cache or empty owner disables reuse.
func serviceHomeworksSettled(ctx context.Context, client EducoderClient, course string, homeworks []educoder.Homework, settled *settledProgressCache, owner string) []homeworkDTO {
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
	candidates := make([]int, 0, min(len(homeworks), 100))
	for i := range result {
		if !result[i].AutomaticSupported {
			continue
		}
		if required, lateUnscored, ok := settled.lookup(settledKey(owner, course, homeworks[i])); ok {
			applyRequiredCounts(&result[i], required, required)
			result[i].LateUnscored = lateUnscored
			continue
		}
		candidates = append(candidates, i)
		if len(candidates) == 100 {
			break
		}
	}
	indexes := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < min(len(candidates), 4); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indexes {
				if ctx.Err() != nil {
					continue
				}
				progress, err := reader.HomeworkProgress(ctx, course, homeworks[index].ID)
				if err != nil {
					continue
				}
				// The assignment score list includes every question kind. Header
				// metadata can omit objective levels and cannot shrink its scope.
				applyRequiredProgress(&result[index], homeworks[index], progress)
				if dto := result[index]; dto.RequiredKnown && dto.RequiredCount != nil && dto.FinishedRequired != nil {
					settled.remember(settledKey(owner, course, homeworks[index]), *dto.RequiredCount, *dto.FinishedRequired, dto.LateUnscored)
				}
			}
		}()
	}
send:
	for _, i := range candidates {
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
		if homework.AutomaticSupported && homework.RequiredKnown && homework.RequiredCount != nil && *homework.RequiredCount == 0 {
			continue
		}
		visible = append(visible, homework)
	}
	return visible
}

func applyRequiredProgress(dto *homeworkDTO, homework educoder.Homework, progress *educoder.HomeworkProgress) {
	if !dto.AutomaticSupported {
		return
	}
	required, completed, valid := educoder.RequiredHomeworkCounts(homework, progress)
	if !valid {
		return
	}
	applyRequiredCounts(dto, required, completed)
	for _, level := range progress.Challenges {
		if !level.IsOptional() && level.LateUnscored(homework) {
			dto.LateUnscored = true
		}
	}
}

func applyRequiredCounts(dto *homeworkDTO, required, completed int) {
	dto.RequiredKnown = true
	dto.RequiredCount, dto.FinishedRequired = &required, &completed
}
