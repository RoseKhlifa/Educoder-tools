package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCollectionNoDefaultDeadlineAndExplicitBudgetValidation(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	for _, limits := range []CollectionLimits{{}, {Pages: 6, Courses: 21, Homeworks: 101, Levels: 501, Timeout: 4 * time.Minute}} {
		h.Limits = limits
		got, err := h.Scan(context.Background(), f, owner, nil)
		if err != nil || !got.ScanExhausted {
			t.Fatalf("explicit compatibility %+v: %+v %v", limits, got, err)
		}
	}
	h.Limits.Timeout = -1
	if _, err := h.Scan(context.Background(), f, owner, nil); err == nil {
		t.Fatal("accepted negative timeout")
	}
}

type overlappingCollectionPages struct {
	*collectionFixture
	mode string
}

func (f *overlappingCollectionPages) CollectionCoursesPage(ctx context.Context, status string, page int) ([]educoder.Course, error) {
	if status != "processing" {
		return nil, nil
	}
	if f.mode == "course_stalled" || f.mode == "course_metadata_changes" {
		return []educoder.Course{{Identifier: "C", Name: fmt.Sprint(page)}}, nil
	}
	if page == 1 {
		return []educoder.Course{{Identifier: "C"}, {Identifier: "C2"}}, nil
	}
	if page == 2 {
		return []educoder.Course{{Identifier: "C2"}}, nil
	}
	if page == 3 {
		return []educoder.Course{{Identifier: "C3"}}, nil
	}
	return nil, nil
}
func (f *overlappingCollectionPages) CollectionHomeworksPage(_ context.Context, course string, page int) ([]educoder.Homework, error) {
	if f.mode == "homework_stalled" || f.mode == "homework_metadata_changes" {
		return []educoder.Homework{{ID: 1, Name: fmt.Sprint(page)}}, nil
	}
	if page == 1 {
		return []educoder.Homework{{ID: 1}, {ID: 2}}, nil
	}
	if page == 2 {
		return []educoder.Homework{{ID: 2}}, nil
	}
	if page == 3 {
		return []educoder.Homework{{ID: 3}}, nil
	}
	return nil, nil
}
func TestCollectionOverlapContinuesButStalledPageFailsIncomplete(t *testing.T) {
	for _, mode := range []string{"overlap", "course_stalled", "homework_stalled", "course_metadata_changes", "homework_metadata_changes"} {
		t.Run(mode, func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			got, err := h.Scan(context.Background(), &overlappingCollectionPages{f, mode}, owner, nil)
			if mode == "overlap" {
				if err != nil || !got.ScanExhausted || got.Courses != 3 || got.Homeworks != 9 {
					t.Fatalf("overlap: %+v %v", got, err)
				}
			} else if err == nil || got.ScanExhausted || got.Failed != 1 || got.LimitReached {
				t.Fatalf("stalled: %+v %v", got, err)
			}
		})
	}
}

func TestCollectionRequestTimeoutIsIncompleteNotWholeScanBudget(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	reader := &collectionCoursePageFixture{collectionFixture: f, read: func(context.Context) error { return context.DeadlineExceeded }}
	got, err := h.Scan(context.Background(), reader, owner, nil)
	if !errors.Is(err, context.DeadlineExceeded) || got.LimitReached || got.ScanExhausted || got.Failed != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

type collectionFreshReadFailure struct {
	*collectionFixture
	calls       int
	readErr     error
	changeScore bool
}

func (f *collectionFreshReadFailure) HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
	f.calls++
	if f.calls > 1 {
		if f.readErr != nil {
			return nil, f.readErr
		}
		if f.changeScore {
			return &educoder.HomeworkProgress{}, nil
		}
	}
	return f.progress, nil
}
func TestCollectionFreshScoreReadFailuresNeverReportExhausted(t *testing.T) {
	for _, kind := range []string{"objective", "programming"} {
		for _, failure := range []error{errors.New("upstream403 synthetic"), context.DeadlineExceeded} {
			t.Run(fmt.Sprintf("%s/%v", kind, failure), func(t *testing.T) {
				h, f, owner := collectionTestSetup(t)
				if kind == "programming" {
					installCollectionCode(t, h, f, owner, "G", true)
				}
				reader := &collectionFreshReadFailure{collectionFixture: f, readErr: failure}
				got, err := h.Scan(context.Background(), reader, owner, nil)
				if err != nil || got.ScanExhausted || got.LimitReached || got.Failed != 1 || got.Reasons["answer_capture_failed"] != 1 || got.Reasons["score_changed_or_unconfirmed"] != 0 || got.Collected != 0 || got.Duplicates != 0 {
					t.Fatalf("readfailure disguised as unconfirmed: %+v %v", got, err)
				}
			})
		}
	}
	h, f, owner := collectionTestSetup(t)
	got, err := h.Scan(context.Background(), &collectionFreshReadFailure{collectionFixture: f, changeScore: true}, owner, nil)
	if err != nil || !got.ScanExhausted || got.Failed != 0 || got.Reasons["score_changed_or_unconfirmed"] != 1 || got.Collected != 0 {
		t.Fatalf("semantic score change no longer distinguished: %+v %v", got, err)
	}
}

type collectionLocatorReadFailure struct{ *collectionFixture }

func (f *collectionLocatorReadFailure) CollectionLocate(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (educoder.CollectionLocationResult, error) {
	return educoder.CollectionLocationResult{}, errors.New("synthetic locator read failure")
}
func TestCollectionLocatorReadFailureIsIncompleteEvenWithWorkingSeed(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	got, err := h.Scan(context.Background(), &collectionLocatorReadFailure{f}, owner, nil)
	if err != nil || got.ScanExhausted || got.Failed != 1 || got.Reasons["readonly_locator_read_failed"] != 1 || got.Collected != 1 {
		t.Fatalf("locator failed but falsely complete: %+v %v", got, err)
	}
}

type collectionMissingCourseIdentity struct{ *collectionFixture }

func (f *collectionMissingCourseIdentity) CollectionCoursesPage(_ context.Context, status string, page int) ([]educoder.Course, error) {
	if status == "processing" && page == 1 {
		return []educoder.Course{{Name: "missing canonical ID"}}, nil
	}
	return nil, nil
}
func TestCollectionUnidentifiedClassroomIsIncomplete(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	got, err := h.Scan(context.Background(), &collectionMissingCourseIdentity{f}, owner, nil)
	if err != nil || got.ScanExhausted || got.Failed != 1 || got.Reasons["classroom_identifier_missing"] != 1 {
		t.Fatalf("unscanned classroom falsely complete: %+v %v", got, err)
	}
}
