package httpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminAssetListQuerySQLite(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "asset-list", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bookmark, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/asset-list"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id         int
		file, name string
		status     string
	}{
		{11, "snapshots/shared name.html", "", "complete"},
		{12, "other.html", "Other", "pending"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset
			(id, date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
			VALUES (?, ?, ?, 10, 'snapshot', 'text/html', ?, ?, 0, ?)`, row.id, time.Now().UTC(), row.file, row.name, row.status, bookmark.ID); err != nil {
			t.Fatal(err)
		}
	}
	query, args := adminAssetListQuery("sqlite", `"shared name"`, "complete")
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected the matching asset")
	}
	var id int
	var displayName string
	var created time.Time
	var status string
	if err := rows.Scan(&id, &displayName, &created, &status); err != nil {
		t.Fatal(err)
	}
	if id != 11 || displayName != "Bookmark Asset #11" || status != "complete" {
		t.Fatalf("got asset %d %q (%s), want fallback name for complete asset 11", id, displayName, status)
	}
	if rows.Next() {
		t.Fatal("unexpected additional matching asset")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	query, args = adminAssetListQuery("sqlite", `"Bookmark Asset #11"`, "complete")
	var fallbackMatches int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+`) AS filtered`, args...).Scan(&fallbackMatches); err != nil || fallbackMatches != 0 {
		t.Fatalf("display fallback should not be searchable: count=%d err=%v", fallbackMatches, err)
	}
}

func TestAdminAssetListQueryPostgres(t *testing.T) {
	query, args := adminAssetListQuery("postgres", `"A name" 100%`, "complete")
	if len(args) != 5 || args[0] != "complete" || args[1] != "%A name%" || args[2] != "%A name%" || args[3] != `%100\%%` || args[4] != `%100\%%` {
		t.Fatalf("unexpected query args: %#v", args)
	}
	if !strings.Contains(query, "status = $1") || !strings.Contains(query, "display_name") || !strings.Contains(query, "file ILIKE $") || !strings.Contains(query, `ESCAPE '\'`) {
		t.Fatalf("unexpected PostgreSQL asset list query: %s", query)
	}
}
