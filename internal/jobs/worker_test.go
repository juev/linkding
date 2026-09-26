package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
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

func TestWorkerLanesRunNormalJobsInParallelAndSnapshotsSerially(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		for _, kind := range []string{"process_snapshot", "refresh_metadata", "refresh_metadata", "process_snapshot"} {
			if _, err := q.Enqueue(ctx, kind, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		started := make(chan string, 3)
		release := make(chan struct{})
		handler := func(_ context.Context, job Job) error {
			started <- job.Kind
			<-release
			return nil
		}
		base := Worker{Queue: q, Lease: time.Minute, MaxAttempts: 6,
			Handlers: map[string]Handler{"refresh_metadata": handler, "process_snapshot": handler}}
		normal := base
		normal.Kind, normal.ExcludeKind = "process_snapshot", true
		snapshot := base
		snapshot.Kind = "process_snapshot"
		var workers sync.WaitGroup
		results := make(chan error, 3)
		for _, worker := range []Worker{normal, normal, snapshot} {
			workers.Add(1)
			go func(w Worker) {
				defer workers.Done()
				processed, err := w.ProcessOne(ctx)
				if err != nil {
					results <- err
				} else if !processed {
					results <- errors.New("worker found no job")
				}
			}(worker)
		}
		kinds := map[string]int{}
		for range 3 {
			select {
			case kind := <-started:
				kinds[kind]++
			case <-time.After(5 * time.Second):
				close(release)
				workers.Wait()
				t.Fatal("three jobs did not start concurrently")
			}
		}
		if kinds["refresh_metadata"] != 2 || kinds["process_snapshot"] != 1 {
			t.Errorf("concurrent job kinds: %v", kinds)
		}
		close(release)
		workers.Wait()
		close(results)
		for err := range results {
			t.Error(err)
		}
		processed, err := snapshot.ProcessOne(ctx)
		if err != nil || !processed {
			t.Fatalf("second snapshot: processed=%t err=%v", processed, err)
		}
		if pending, err := q.Claim(ctx, time.Minute); err != nil || pending != nil {
			t.Fatalf("queue not drained: %v, %v", pending, err)
		}
	})
}
