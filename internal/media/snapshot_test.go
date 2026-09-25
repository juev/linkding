package media

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/store"
)

func TestSnapshotJobWritesPDFAndHTMLAndRecordsFailure(t *testing.T) {
	const pdfData = "%PDF-1.7\nfixture PDF"
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Length", fmt.Sprint(len(pdfData)))
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte(pdfData))
			}
		} else {
			w.Header().Set("Content-Type", "text/html")
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("<html>source</html>"))
			}
		}
	}))
	defer web.Close()
	script := filepath.Join(t.TempDir(), "single-file")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor last; do :; done\nprintf '<html>snapshot</html>' > \"$last\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := config.Config{
		DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1",
		EnableSnapshots: true, SnapshotPDFMaxSize: 1024, SingleFilePath: script,
		SingleFileTimeoutSec: 2, SingleFileUblockOptions: `'--browser-arg="--headless=new"'`,
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "snapshots", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_automatic_html_snapshots = 1 WHERE user_id = ?", user.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepositoryWithTasks(db, "sqlite", bookmarks.TaskPolicy{SnapshotsEnabled: true})
	processor := New(db, "sqlite", cfg)
	assetFor := func(path string) (int64, int64) {
		t.Helper()
		item, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: web.URL + path})
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := db.QueryRowContext(ctx, "SELECT id FROM bookmarks_bookmarkasset WHERE bookmark_id = ?", item.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return item.ID, id
	}
	verify := func(bookmarkID, assetID int64, wantType, wantContent string) {
		t.Helper()
		var status, filename, contentType string
		var gz bool
		if err := db.QueryRowContext(ctx, "SELECT status, file, content_type, gzip FROM bookmarks_bookmarkasset WHERE id = ?", assetID).Scan(&status, &filename, &contentType, &gz); err != nil {
			t.Fatal(err)
		}
		if status != "complete" || filename == "" || contentType != wantType || !gz {
			t.Fatalf("asset %d: %q %q %q gzip=%t", assetID, status, filename, contentType, gz)
		}
		file, err := os.Open(filepath.Join(cfg.DataDir, "assets", filename))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		reader, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || string(content) != wantContent {
			t.Fatalf("asset content: %q %v", content, err)
		}
		var latest int64
		if err := db.QueryRowContext(ctx, "SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id = ?", bookmarkID).Scan(&latest); err != nil || latest != assetID {
			t.Fatalf("latest snapshot %d, err=%v", latest, err)
		}
	}
	pdfBookmark, pdfAsset := assetFor("/pdf")
	if err := processor.ProcessSnapshot(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"asset_id":%d}`, pdfAsset))}); err != nil {
		t.Fatal(err)
	}
	verify(pdfBookmark, pdfAsset, "application/pdf", pdfData)
	htmlBookmark, htmlAsset := assetFor("/html")
	if err := processor.ProcessSnapshot(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"asset_id":%d}`, htmlAsset))}); err != nil {
		t.Fatal(err)
	}
	verify(htmlBookmark, htmlAsset, "text/html", "<html>snapshot</html>")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, failedAsset := assetFor("/failure")
	job := jobs.Job{Payload: []byte(fmt.Sprintf(`{"asset_id":%d}`, failedAsset))}
	if err := processor.ProcessSnapshot(ctx, job); err == nil || !strings.Contains(err.Error(), "single-file") {
		t.Fatalf("missing failure: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, "SELECT status FROM bookmarks_bookmarkasset WHERE id = ?", failedAsset).Scan(&status); err != nil || status != "failure" {
		t.Fatalf("failed asset status=%q err=%v", status, err)
	}
	if err := processor.ProcessSnapshot(ctx, job); err != nil {
		t.Fatalf("failed asset was processed again: %v", err)
	}
}

func TestQueuedSnapshotRunsThroughWorker(t *testing.T) {
	const pdfData = "%PDF-1.7\nqueued snapshot"
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Length", fmt.Sprint(len(pdfData)))
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(pdfData))
		}
	}))
	defer web.Close()
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1", EnableSnapshots: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "queued", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET enable_favicons = 0,
		enable_preview_images = 0, web_archive_integration = 'disabled',
		enable_automatic_html_snapshots = 1 WHERE user_id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepositoryWithTasks(db, "sqlite", bookmarks.TaskPolicy{SnapshotsEnabled: true})
	item, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: web.URL + "/pdf"})
	if err != nil {
		t.Fatal(err)
	}
	processor := New(db, "sqlite", cfg)
	worker := jobs.Worker{Queue: jobs.New(db, "sqlite"), Handlers: processor.Handlers(), Lease: time.Minute, MaxAttempts: 6}
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("process queued snapshot: processed=%t err=%v", processed, err)
	}
	var status, filename, jobStatus string
	if err := db.QueryRowContext(ctx, `SELECT a.status, a.file, j.status FROM bookmarks_bookmarkasset a
		JOIN linkding_job j ON j.kind = 'process_snapshot' WHERE a.bookmark_id = ?`, item.ID).Scan(&status, &filename, &jobStatus); err != nil {
		t.Fatal(err)
	}
	if status != "complete" || filename == "" || jobStatus != "complete" {
		t.Fatalf("asset=%q file=%q job=%q", status, filename, jobStatus)
	}
	file, err := os.Open(filepath.Join(cfg.DataDir, "assets", filename))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != pdfData {
		t.Fatalf("saved PDF: %q err=%v", content, err)
	}
}

func TestSnapshotDisabledOrCanceledKeepsAssetPending(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>fixture</html>"))
	}))
	defer web.Close()
	script := filepath.Join(t.TempDir(), "single-file")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1", EnableSnapshots: true, SingleFilePath: script, SingleFileTimeoutSec: 20}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "pending", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepositoryWithTasks(db, "sqlite", bookmarks.TaskPolicy{SnapshotsEnabled: true}).CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: web.URL})
	if err != nil {
		t.Fatal(err)
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM bookmarks_bookmarkasset WHERE bookmark_id = ?", item.ID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{Payload: []byte(fmt.Sprintf(`{"asset_id":%d}`, assetID))}
	cfg.EnableSnapshots = false
	if err := New(db, "sqlite", cfg).ProcessSnapshot(ctx, job); err != nil {
		t.Fatal(err)
	}
	cfg.EnableSnapshots = true
	canceled, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := New(db, "sqlite", cfg).ProcessSnapshot(canceled, job); err == nil {
		t.Fatal("expected cancellation")
	}
	var status string
	if err := db.QueryRowContext(ctx, "SELECT status FROM bookmarks_bookmarkasset WHERE id = ?", assetID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("pending asset after cancellation: status=%q err=%v", status, err)
	}
	count, err := RequeuePendingSnapshots(ctx, db, "sqlite")
	if err != nil || count != 1 {
		t.Fatalf("requeue pending snapshot: count=%d err=%v", count, err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nfor last; do :; done\nprintf '<html>recovered</html>' > \"$last\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	worker := jobs.Worker{Queue: jobs.New(db, "sqlite"), Handlers: New(db, "sqlite", cfg).Handlers(), Lease: time.Minute, MaxAttempts: 6}
	for range 2 {
		processed, err := worker.ProcessOne(ctx)
		if err != nil || !processed {
			t.Fatalf("recovered worker: processed=%t err=%v", processed, err)
		}
	}
	if err := db.QueryRowContext(ctx, "SELECT status FROM bookmarks_bookmarkasset WHERE id = ?", assetID).Scan(&status); err != nil || status != "complete" {
		t.Fatalf("recovered asset: status=%q err=%v", status, err)
	}
}
