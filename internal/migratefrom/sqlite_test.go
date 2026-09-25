package migratefrom

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestInspectSQLiteRequiresPinnedMigrationAndCountsData(t *testing.T) {
	ctx := context.Background()
	sourceDir := t.TempDir()
	cfg := config.Config{DBEngine: "sqlite", DataDir: sourceDir}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSQLite(ctx, sourceDir); err == nil || !strings.Contains(err.Error(), "v1.47.0") {
		t.Fatalf("old schema preflight: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO django_migrations (app, name, applied) VALUES ('bookmarks', ?, ?)`,
		upstreamBookmarkMigration, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "migration", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark
		(url, url_normalized, title, description, notes, website_title, website_description, unread, is_archived,
		 shared, date_added, date_modified, owner_id, web_archive_snapshot_url, favicon_file, preview_image_file)
		 VALUES ('https://example.com', 'https://example.com', '', '', '', NULL, NULL, 0, 0, 0, ?, ?, ?, '', '', '')`,
		time.Now().UTC(), time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	report, err := InspectSQLite(ctx, sourceDir)
	if err != nil || report.UserCount != 1 || report.BookmarkCount != 1 || report.PendingTasks != 0 || report.ScheduledTasks != 0 {
		t.Fatalf("preflight: %+v, err=%v", report, err)
	}
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'existing', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sourceDir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "assets", "sample.txt"), []byte("original asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"linkding_lock", "linkding_job", "goose_db_version"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "migrated-data")
	migrated, err := MigrateSQLite(ctx, sourceDir, target)
	if err != nil || migrated.CopiedFiles != 1 || migrated.UserCount != 1 || migrated.BookmarkCount != 1 {
		t.Fatalf("migration: %+v, err=%v", migrated, err)
	}
	data, err := os.ReadFile(filepath.Join(target, "assets", "sample.txt"))
	if err != nil || string(data) != "original asset" {
		t.Fatalf("copied asset: %q, err=%v", data, err)
	}
	copyDB, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: target})
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	if _, err := auth.NewRepository(copyDB, "sqlite").AuthenticatePassword(ctx, "migration", "password"); err != nil {
		t.Fatalf("migrated password: %v", err)
	}
	if _, err := auth.NewRepository(copyDB, "sqlite").AuthenticateToken(ctx, token); err != nil {
		t.Fatalf("migrated API token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "assets", "sample.txt")); err != nil {
		t.Fatalf("source data changed: %v", err)
	}
	queue, err := sql.Open("sqlite", filepath.Join(sourceDir, "tasks.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	for _, statement := range []string{
		`CREATE TABLE task (id integer primary key, queue text not null, data blob not null, priority real not null)`,
		`CREATE TABLE schedule (id integer primary key, queue text not null, data blob not null, timestamp real not null)`,
		`INSERT INTO task (id, queue, data, priority) VALUES (1, 'default', x'01', 0)`,
	} {
		if _, err := queue.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	blockedTarget := filepath.Join(t.TempDir(), "blocked")
	if _, err := MigrateSQLite(ctx, sourceDir, blockedTarget); err == nil || !strings.Contains(err.Error(), "Huey queue is not empty") {
		t.Fatalf("migration with pending task: %v", err)
	}
	if _, err := os.Stat(blockedTarget); !os.IsNotExist(err) {
		t.Fatalf("migration created target before draining queue: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET favicon_file='missing.ico' WHERE owner_id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSQLite(ctx, sourceDir); err == nil || !strings.Contains(err.Error(), "source favicons") {
		t.Fatalf("missing file reference was accepted: %v", err)
	}
}

func TestMigratePinnedUpstreamSQLiteFixture(t *testing.T) {
	sourceDir := os.Getenv("LINKDING_TEST_SOURCE_SQLITE_DATA")
	if sourceDir == "" {
		t.Skip("set LINKDING_TEST_SOURCE_SQLITE_DATA to a stopped v1.47.0 data directory copy")
	}
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "migrated")
	report, err := MigrateSQLite(ctx, sourceDir, target)
	if err != nil {
		t.Fatal(err)
	}
	if report.BookmarkCount == 0 || report.UserCount == 0 || report.CopiedFiles == 0 {
		t.Fatalf("fixture was not populated: %+v", report)
	}
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: target})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("normal startup after migration: %v", err)
	}
}
