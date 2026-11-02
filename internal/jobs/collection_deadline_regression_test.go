package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// This regression deliberately uses only the original Manager API so it can be
// copied to the previous release and reproduce its actual collection timeout.
func TestCollectionScanOutlivesAnswerDeadline(t *testing.T) {
	m := testManager(t, Config{Workers: 2, Timeout: 50 * time.Millisecond})
	started := make(chan struct{})
	var collectionHasDeadline atomic.Bool
	collection := submit(t, m, "collector", Request{Mode: "collect_verified"}, func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		_, hasDeadline := ctx.Deadline()
		collectionHasDeadline.Store(hasDeadline)
		close(started)
		select {
		case <-time.After(200 * time.Millisecond):
			return Outcome{Status: Succeeded}, nil
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		}
	})
	receive(t, started)
	answer := submit(t, m, "answer", request("deadline"), func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})
	answerResult := waitJob(t, m, "answer", answer.ID, Failed)
	if answerResult.ErrorCode != "timeout" {
		t.Fatalf("ordinary answer deadline lost: %+v", answerResult)
	}
	result := waitJob(t, m, "collector", collection.ID, Succeeded)
	if collectionHasDeadline.Load() {
		t.Fatal("collection inherited an overall job deadline")
	}
	t.Logf("ANSWER_ERROR=%s; COLLECTION_STATUS=%s; COLLECTION_HAS_DEADLINE=%t", answerResult.ErrorCode, result.Status, collectionHasDeadline.Load())
}
