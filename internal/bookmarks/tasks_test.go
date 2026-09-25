package bookmarks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkAndBackgroundTasksCommitTogether(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "jobs", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET
		enable_favicons = 1, enable_preview_images = 1, web_archive_integration = 'enabled',
		enable_automatic_html_snapshots = 1 WHERE user_id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	repo := NewRepositoryWithTasks(db, "sqlite", TaskPolicy{SnapshotsEnabled: true})
	item, created, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/new"})
	if err != nil || !created {
		t.Fatalf("create: %+v %t %v", item, created, err)
	}
	kinds := func() []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, "SELECT kind FROM linkding_job ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var kind string
			if err := rows.Scan(&kind); err != nil {
				t.Fatal(err)
			}
			result = append(result, kind)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	assertKinds := func(want ...string) {
		t.Helper()
		got := kinds()
		if len(got) != len(want) {
			t.Fatalf("job kinds %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("job kinds %v, want %v", got, want)
			}
		}
	}
	assertKinds("web_archive_snapshot", "load_favicon", "load_preview_image", "process_snapshot")
	var status, file string
	if err := db.QueryRowContext(ctx, "SELECT status, file FROM bookmarks_bookmarkasset WHERE bookmark_id = ?", item.ID).Scan(&status, &file); err != nil || status != "pending" || file != "" {
		t.Fatalf("pending snapshot: status=%q file=%q err=%v", status, file, err)
	}
	_, created, err = repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/new", Title: "Duplicate"})
	if err != nil || created {
		t.Fatalf("duplicate: created=%t err=%v", created, err)
	}
	assertKinds("web_archive_snapshot", "load_favicon", "load_preview_image", "process_snapshot", "load_favicon", "load_preview_image")
	changed := "https://example.com/changed"
	if _, err := repo.UpdateData(ctx, user.ID, item.ID, UpdateInput{URL: &changed}); err != nil {
		t.Fatal(err)
	}
	assertKinds("web_archive_snapshot", "load_favicon", "load_preview_image", "process_snapshot", "load_favicon", "load_preview_image", "load_favicon", "load_preview_image", "web_archive_snapshot")
	var payload string
	if err := db.QueryRowContext(ctx, "SELECT payload FROM linkding_job ORDER BY id DESC LIMIT 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var archive struct {
		BookmarkID  int64 `json:"bookmark_id"`
		ForceUpdate bool  `json:"force_update"`
	}
	if err := json.Unmarshal([]byte(payload), &archive); err != nil || archive.BookmarkID != item.ID || !archive.ForceUpdate {
		t.Fatalf("URL change archive payload: %+v %v", archive, err)
	}
	if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/no-snapshot", DisableHTMLSnapshot: true}); err != nil {
		t.Fatal(err)
	}
	var assets int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM bookmarks_bookmarkasset").Scan(&assets); err != nil || assets != 1 {
		t.Fatalf("disabled snapshot count=%d err=%v", assets, err)
	}
	priorJobs := len(kinds())
	disabled := NewRepositoryWithTasks(db, "sqlite", TaskPolicy{BackgroundDisabled: true, SnapshotsEnabled: true})
	if _, _, err := disabled.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/no-background"}); err != nil {
		t.Fatal(err)
	}
	if len(kinds()) != priorJobs {
		t.Fatal("background-disabled create enqueued a job")
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE linkding_job"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/rollback"}); err == nil {
		t.Fatal("missing job queue should roll back bookmark")
	}
	var rollbackCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM bookmarks_bookmark WHERE url = ?", "https://example.com/rollback").Scan(&rollbackCount); err != nil || rollbackCount != 0 {
		t.Fatalf("bookmark committed without job: count=%d err=%v", rollbackCount, err)
	}
}
