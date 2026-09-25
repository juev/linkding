package importexport

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func importFixture(t *testing.T) (context.Context, *sql.DB, config.Config, int64) {
	t.Helper()
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "importer", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, cfg, user.ID
}

func TestImportNetscapeMergesExistingAndSkipsBadRows(t *testing.T) {
	ctx, db, cfg, ownerID := importFixture(t)
	first := `<DL><DT><A HREF="https://example.com/" ADD_DATE="1000" LAST_MODIFIED="2000" TAGS="Original" PRIVATE="1" TOREAD="1">Original title</A><DD>Original description[linkding-notes]Original notes[/linkding-notes]</DL>`
	result, err := ImportNetscape(ctx, db, cfg, ownerID, first, ImportOptions{})
	if err != nil || result != (ImportResult{Total: 1, Success: 1}) {
		t.Fatalf("first import: %+v, %v", result, err)
	}
	second := `<DL><DT><A HREF="https://EXAMPLE.com" ADD_DATE="3000" LAST_MODIFIED="4000" TAGS="New,linkding:bookmarks.archived" PRIVATE="0" TOREAD="0"></A>` +
		`<DT><A HREF="https://example.com/" ADD_DATE="5">Duplicate</A>` +
		`<DT><A HREF="invalid.example" TAGS="Orphan">Invalid</A>` +
		`<DT><A HREF="https://other.example" ADD_DATE="not-a-date">Bad date</A></DL>`
	result, err = ImportNetscape(ctx, db, cfg, ownerID, second, ImportOptions{MapPrivateFlag: true})
	if err != nil || result != (ImportResult{Total: 4, Success: 1, Failed: 3}) {
		t.Fatalf("second import: %+v, %v", result, err)
	}
	items, total, err := bookmarks.NewRepository(db, "sqlite").ListPage(ctx, ownerID, false, 20, 0)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("bookmarks: %+v total=%d err=%v", items, total, err)
	}
	item := items[0]
	if item.URL != "https://EXAMPLE.com" || item.Title != "Original title" || item.Description != "Original description" || item.Notes != "Original notes" ||
		item.Unread || !item.Shared || item.IsArchived || !slices.Equal(item.TagNames, []string{"New", "Original"}) ||
		!item.DateAdded.Equal(time.Unix(3000, 0)) || !item.DateModified.Equal(time.Unix(4000, 0)) {
		t.Fatalf("merged bookmark: %+v", item)
	}
	var orphanTags int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_tag WHERE name = 'Orphan' AND owner_id = ?`, ownerID).Scan(&orphanTags); err != nil || orphanTags != 1 {
		t.Fatalf("tags from invalid rows are precreated: count=%d err=%v", orphanTags, err)
	}
}

func TestImportedTimestampPreservesFractionalSeconds(t *testing.T) {
	for _, testcase := range []struct {
		raw  string
		want time.Time
	}{
		{"1700000000123", time.Unix(1700000000, 123000000)},
		{"1700000000123456", time.Unix(1700000000, 123456000)},
	} {
		got, err := importedTimestamp(testcase.raw)
		if err != nil || !got.Equal(testcase.want) {
			t.Errorf("timestamp %s: got %s, want %s, err %v", testcase.raw, got, testcase.want, err)
		}
	}
	for _, raw := range []string{"bad", "1700000000123456789"} {
		if _, err := importedTimestamp(raw); err == nil {
			t.Errorf("invalid timestamp %q was accepted", raw)
		}
	}
}

func TestImportedURLValidatorAllowsCredentialsLikeUpstream(t *testing.T) {
	item := NetscapeBookmark{Href: "https://alice:secret@example.com/path"}
	if !validImportedBookmark(item, false) {
		t.Fatal("upstream URLValidator accepts URL userinfo")
	}
}

func TestImportNetscapePostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "postgres").CreateUser(ctx, auth.NewUser{
		Username: fmt.Sprintf("import_parity_%d", time.Now().UnixNano()), Password: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id = $1)`,
			`DELETE FROM bookmarks_bookmark WHERE owner_id = $1`,
			`DELETE FROM bookmarks_tag WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("cleanup PostgreSQL import fixture: %v", err)
			}
		}
	})
	cfg := config.Config{DBEngine: "postgres", DisableBackgroundTasks: true}
	result, err := ImportNetscape(ctx, db, cfg, user.ID,
		`<DL><DT><A HREF="https://pg.example" ADD_DATE="1" TAGS="postgres">PG</A></DL>`, ImportOptions{})
	if err != nil || result != (ImportResult{Total: 1, Success: 1}) {
		t.Fatalf("PostgreSQL import: %+v, err=%v", result, err)
	}
	items, total, err := bookmarks.NewRepository(db, "postgres").ListPage(ctx, user.ID, false, 10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].Title != "PG" || !slices.Equal(items[0].TagNames, []string{"postgres"}) {
		t.Fatalf("PostgreSQL imported bookmarks: total=%d items=%+v err=%v", total, items, err)
	}
}
