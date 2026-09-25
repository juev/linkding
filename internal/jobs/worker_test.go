package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestWorkerProcessesAndRetries(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, "test", json.RawMessage(`{"value":1}`)); err != nil {
			t.Fatal(err)
		}
		calls := 0
		w := Worker{Queue: q, Lease: time.Second, MaxAttempts: 6, RetryDelay: func(int) time.Duration { return 0 }, Handlers: map[string]Handler{
			"test": func(_ context.Context, job Job) error {
				calls++
				if job.Kind != "test" || !json.Valid(job.Payload) {
					t.Fatalf("unexpected job: %+v", job)
				}
				if calls == 1 {
					return errors.New("transient")
				}
				return nil
			},
		}}
		processed, err := w.ProcessOne(ctx)
		if !processed || err == nil {
			t.Fatalf("first processing: processed=%t err=%v", processed, err)
		}
		processed, err = w.ProcessOne(ctx)
		if !processed || err != nil || calls != 2 {
			t.Fatalf("retry processing: processed=%t err=%v calls=%d", processed, err, calls)
		}
		processed, err = w.ProcessOne(ctx)
		if processed || err != nil {
			t.Fatalf("completed job reprocessed: processed=%t err=%v", processed, err)
		}
	})
}

func TestWorkerStopsOnCanceledContext(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w := Worker{Queue: q, Lease: time.Second, MaxAttempts: 6, Handlers: map[string]Handler{}}
		if err := w.Run(ctx, time.Hour); err != nil {
			t.Fatalf("canceled worker: %v", err)
		}
	})
}
