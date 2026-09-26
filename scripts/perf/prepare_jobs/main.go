package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/juev/linkding/internal/jobs"
	_ "modernc.org/sqlite"
)

func main() {
	dataDir := flag.String("data-dir", "", "migrated Go data directory")
	origin := flag.String("origin", "", "mock site origin reachable from the container")
	count := flag.Int("count", 100, "number of metadata jobs")
	flag.Parse()
	if *dataDir == "" || *origin == "" || *count < 1 {
		fail("data-dir, origin, and positive count are required")
	}
	parsed, err := url.Parse(*origin)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		fail("origin must be an HTTP URL")
	}
	dbPath, err := filepath.Abs(filepath.Join(*dataDir, "db.sqlite3"))
	if err != nil {
		fail("database path: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fail("open database: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	queue := jobs.New(db, "sqlite")
	for bookmarkID := 1; bookmarkID <= *count; bookmarkID++ {
		pageURL := fmt.Sprintf("%s/go/page/%d", strings.TrimRight(*origin, "/"), bookmarkID)
		result, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET url = ?, url_normalized = ?, title = '' WHERE id = ?`, pageURL, pageURL, bookmarkID)
		if err != nil {
			fail("update bookmark %d: %v", bookmarkID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			fail("bookmark %d missing: rows=%d error=%v", bookmarkID, rows, err)
		}
		payload, err := json.Marshal(map[string]int{"bookmark_id": bookmarkID})
		if err != nil {
			fail("encode job %d: %v", bookmarkID, err)
		}
		if _, err := queue.Enqueue(ctx, "refresh_metadata", payload); err != nil {
			fail("enqueue job %d: %v", bookmarkID, err)
		}
	}
	fmt.Printf("queued metadata jobs=%d\n", *count)
}

func fail(format string, values ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", values...)
	os.Exit(1)
}
