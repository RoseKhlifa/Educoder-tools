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

type homeworkAssignedCountsReader interface {
	AssignedHomeworkCounts(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool)
}

// serviceHomeworks enriches only executable shixun rows with required-level
// progress. Common text/image assignments have a different submission protocol
// and must remain visible without shixun progress requests or optional-level
// filtering. Unknown or partial progress cannot claim required-work completion.
func serviceHomeworks(ctx context.Context, client EducoderClient, course string, homeworks []educoder.Homework) []homeworkDTO {
	result := make([]homeworkDTO, len(homeworks))
	for i, homework := range homeworks {
		result[i] = toHomeworkDTO(homework)
	}
	reader, ok := client.(homeworkProgressReader)
	if !ok {
		return result
	}
	assignedReader, hasAssignedReader := client.(homeworkAssignedCountsReader)
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	candidates := make([]int, 0, min(len(homeworks), 100))
	for i := range result {
		if result[i].AutomaticSupported {
			candidates = append(candidates, i)
			if len(candidates) == 100 {
				break
			}
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
				required, completed, valid := educoder.RequiredHomeworkCounts(homeworks[index], progress)
				// A fully verified catalog already proves completion. Pending or
				// unknown catalog counts need the same assignment projection as
				// execution: score responses may contain unassigned zeroes.
				if hasAssignedReader && (!valid || completed != required) {
					valid = false
					if ctx.Err() == nil {
						required, completed, valid = assignedReader.AssignedHomeworkCounts(ctx, course, homeworks[index], progress)
					}
				}
				if valid {
					applyRequiredCounts(&result[index], required, completed)
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
}

func applyRequiredCounts(dto *homeworkDTO, required, completed int) {
	dto.RequiredKnown = true
	dto.RequiredCount, dto.FinishedRequired = &required, &completed
}
