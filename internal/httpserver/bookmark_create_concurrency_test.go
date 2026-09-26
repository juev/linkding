package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkCreateConcurrentSQLite(t *testing.T) {
	for _, concurrency := range []int{8, 32} {
		t.Run(fmt.Sprintf("unique_urls/concurrency_%d", concurrency), func(t *testing.T) {
			handler, db, userID, token := newConcurrentBookmarkCreateFixture(t)
			const requestsPerWorker = 4
			requestCount := concurrency * requestsPerWorker
			statuses, ids := concurrentBookmarkCreates(handler, token, requestCount, concurrency, func(i int) string {
				return fmt.Sprintf("https://concurrent.example/%d", i)
			})

			assertCreatedResponses(t, statuses, ids)
			assertBookmarkCount(t, db, userID, requestCount)
		})
	}

	t.Run("same_url/concurrency_32", func(t *testing.T) {
		handler, db, userID, token := newConcurrentBookmarkCreateFixture(t)
		const requestCount = 128
		statuses, ids := concurrentBookmarkCreates(handler, token, requestCount, 32, func(int) string {
			return "https://concurrent.example/shared"
		})

		assertCreatedResponses(t, statuses, ids)
		for i, id := range ids[1:] {
			if id != ids[0] {
				t.Fatalf("response %d returned bookmark ID %d, want duplicate bookmark ID %d", i+1, id, ids[0])
			}
		}
		assertBookmarkCount(t, db, userID, 1)
	})
}

func newConcurrentBookmarkCreateFixture(t *testing.T) (http.Handler, *sql.DB, int64, string) {
	t.Helper()
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/", DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "benchmark", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "cccccccccccccccccccccccccccccccccccccccc"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'concurrency test', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	return New(db, cfg, t.TempDir()), db, user.ID, token
}

func concurrentBookmarkCreates(handler http.Handler, token string, count, concurrency int, urlFor func(int) string) ([]int, []int64) {
	statuses := make([]int, count)
	ids := make([]int64, count)
	start := make(chan struct{})
	jobs := make(chan int, count)
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for i := range jobs {
				body, _ := json.Marshal(map[string]string{"url": urlFor(i)})
				req := httptest.NewRequest(http.MethodPost, "/linkding/api/bookmarks/?disable_scraping=1&disable_html_snapshot=1", bytes.NewReader(body))
				req.Header.Set("Authorization", "Token "+token)
				req.Header.Set("Content-Type", "application/json")
				<-start
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				statuses[i] = response.Code
				if response.Code == http.StatusCreated {
					var bookmark struct {
						ID int64 `json:"id"`
					}
					if json.Unmarshal(response.Body.Bytes(), &bookmark) == nil {
						ids[i] = bookmark.ID
					}
				}
			}
		}()
	}
	for i := range count {
		jobs <- i
	}
	close(jobs)
	close(start)
	workers.Wait()
	return statuses, ids
}

func assertCreatedResponses(t *testing.T, statuses []int, ids []int64) {
	t.Helper()
	for i, code := range statuses {
		if code != http.StatusCreated {
			t.Errorf("request %d returned HTTP %d, want %d", i, code, http.StatusCreated)
		}
		if ids[i] == 0 {
			t.Errorf("request %d returned no bookmark ID", i)
		}
	}
}

func assertBookmarkCount(t *testing.T, db *sql.DB, userID int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT count(*) FROM bookmarks_bookmark WHERE owner_id = ?`, userID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("bookmark count=%d, want %d", got, want)
	}
}
