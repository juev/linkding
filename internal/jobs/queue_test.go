package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func testQueues(t *testing.T, test func(t *testing.T, q *Queue)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		db, err := store.Open(context.Background(), config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := store.Migrate(context.Background(), db, "sqlite"); err != nil {
			t.Fatal(err)
		}
		test(t, New(db, "sqlite"))
	})
	if dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN"); dsn != "" {
		t.Run("postgres", func(t *testing.T) {
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := store.Migrate(context.Background(), db, "postgres"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("DELETE FROM linkding_job"); err != nil {
				t.Fatal(err)
			}
			test(t, New(db, "postgres"))
		})
	}
}

func TestQueueClaimsOnceAndCompletes(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		id, err := q.Enqueue(ctx, "favicon", json.RawMessage(`{"bookmark_id":12}`))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		claimed := make(chan Job, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				job, err := q.Claim(ctx, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if job != nil {
					claimed <- *job
				}
			}()
		}
		wg.Wait()
		close(claimed)
		var got []Job
		for job := range claimed {
			got = append(got, job)
		}
		if len(got) != 1 || got[0].ID != id || got[0].Attempts != 1 || got[0].Kind != "favicon" {
			t.Fatalf("claimed %v", got)
		}
		if err := q.Complete(ctx, got[0]); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Claim(ctx, time.Minute); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueueClaimsSeparateSnapshotAndNormalLanes(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		for _, kind := range []string{"process_snapshot", "refresh_metadata", "process_snapshot"} {
			if _, err := q.Enqueue(ctx, kind, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
		}
		normal, err := q.ClaimByKind(ctx, time.Minute, "process_snapshot", true)
		if err != nil || normal == nil || normal.Kind != "refresh_metadata" {
			t.Fatalf("normal lane claimed %v: %v", normal, err)
		}
		if another, err := q.ClaimByKind(ctx, time.Minute, "process_snapshot", true); err != nil || another != nil {
			t.Fatalf("normal lane took snapshot: %v, %v", another, err)
		}
		for range 2 {
			snapshot, err := q.ClaimByKind(ctx, time.Minute, "process_snapshot", false)
			if err != nil || snapshot == nil || snapshot.Kind != "process_snapshot" {
				t.Fatalf("snapshot lane claimed %v: %v", snapshot, err)
			}
			if err := q.Complete(ctx, *snapshot); err != nil {
				t.Fatal(err)
			}
		}
		if err := q.Complete(ctx, *normal); err != nil {
			t.Fatal(err)
		}
		if remaining, err := q.Claim(ctx, time.Minute); err != nil || remaining != nil {
			t.Fatalf("lane claims left a job: %v, %v", remaining, err)
		}
	})
}

func TestQueueRetryLeaseAndTerminalFailure(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, "snapshot", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		first, err := q.Claim(ctx, 5*time.Millisecond)
		if err != nil || first == nil {
			t.Fatalf("first claim: %v, %v", first, err)
		}
		if err := q.Complete(ctx, Job{ID: first.ID, LeaseToken: "wrong"}); err != ErrLeaseLost {
			t.Fatalf("wrong lease: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
		second, err := q.Claim(ctx, time.Minute)
		if err != nil || second == nil {
			t.Fatalf("expired claim: %v, %v", second, err)
		}
		if second.Attempts != 2 || second.LeaseToken == first.LeaseToken {
			t.Fatalf("renewed claim: %v", second)
		}
		if err := q.Complete(ctx, *first); err != ErrLeaseLost {
			t.Fatalf("stale completion: %v", err)
		}
		if err := q.Fail(ctx, *second, "temporary failure", 2, 0); err != nil {
			t.Fatal(err)
		}
		if next, err := q.Claim(ctx, time.Minute); err != nil || next != nil {
			t.Fatalf("terminal failure claim: %v, %v", next, err)
		}
		var status, reason string
		query := "SELECT status, last_error FROM linkding_job WHERE id = ?"
		if q.engine == "postgres" {
			query = "SELECT status, last_error FROM linkding_job WHERE id = $1"
		}
		if err := q.db.QueryRow(query, first.ID).Scan(&status, &reason); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || reason != "temporary failure" {
			t.Fatalf("status=%q reason=%q", status, reason)
		}
	})
}

func TestQueueRejectsInvalidPayload(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		if _, err := q.Enqueue(context.Background(), "favicon", json.RawMessage(`{broken`)); err == nil {
			t.Fatal("accepted invalid JSON")
		}
	})
}

func TestQueueEnqueueTxRollsBackWithCaller(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		tx, err := q.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.EnqueueTx(ctx, tx, "preview", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if job, err := q.Claim(ctx, time.Minute); err != nil || job != nil {
			t.Fatalf("rolled back job visible: %v, %v", job, err)
		}
	})
}

func TestQueueRetryAfterFailureAndRenewLease(t *testing.T) {
	testQueues(t, func(t *testing.T, q *Queue) {
		ctx := context.Background()
		if _, err := q.Enqueue(ctx, "preview", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		first, err := q.Claim(ctx, time.Minute)
		if err != nil || first == nil {
			t.Fatalf("first claim: %v, %v", first, err)
		}
		if err := q.Renew(ctx, Job{ID: first.ID, LeaseToken: "wrong"}, time.Minute); err != ErrLeaseLost {
			t.Fatalf("wrong renewal: %v", err)
		}
		if err := q.Renew(ctx, *first, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := q.Fail(ctx, *first, "transient", 6, 0); err != nil {
			t.Fatal(err)
		}
		second, err := q.Claim(ctx, time.Minute)
		if err != nil || second == nil {
			t.Fatalf("retry claim: %v, %v", second, err)
		}
		if second.Attempts != 2 {
			t.Fatalf("retry attempts=%d", second.Attempts)
		}
		if err := q.Complete(ctx, *second); err != nil {
			t.Fatal(err)
		}
	})
}
