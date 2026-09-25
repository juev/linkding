package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestImportNetscapeImportsForNamedUser(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data"), DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, cfg.DBEngine).CreateUser(ctx, auth.NewUser{Username: "netscape-owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "bookmarks.html")
	const source = `<DL><DT><A HREF="https://example.com/" ADD_DATE="1000" TAGS="cli">Example</A></DL>`
	if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importNetscape(ctx, cfg, filename, user.Username); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmark WHERE owner_id = ? AND url = ?`, user.ID, "https://example.com/").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("imported bookmarks for owner %d: got %d, want 1", user.ID, count)
	}
}

func TestImportNetscapeReportsMissingFileAndUser(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")}
	filename := filepath.Join(t.TempDir(), "missing.html")
	if err := importNetscape(ctx, cfg, filename, "owner"); err == nil || !strings.Contains(err.Error(), "read Netscape bookmark file") {
		t.Fatalf("missing file error = %v", err)
	}
	if err := os.WriteFile(filename, []byte("<DL></DL>"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := importNetscape(ctx, cfg, filename, "missing-owner")
	if err == nil || !strings.Contains(err.Error(), `user "missing-owner" does not exist`) {
		t.Fatalf("missing user error = %v", err)
	}
}

func TestRunImportNetscapeRequiresFileAndUser(t *testing.T) {
	if err := runImportNetscape(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "usage: linkding import_netscape") {
		t.Fatalf("usage error = %v", err)
	}
}
