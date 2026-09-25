package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestCreateDuplicateUsesPinnedMergeRuleAndOwnerScope(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRepository(db, "sqlite")
	date := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	first, created, err := r.CreateOrUpdateData(ctx, alice.ID, CreateInput{
		URL: "HTTPS://Example.COM/path/?b=2&a=1", Title: "First", Notes: "original",
		Unread: true, Shared: true, TagNames: []string{"Go lang", "Beta"}, DateAdded: &date,
	})
	if err != nil || !created {
		t.Fatalf("first create: bookmark=%+v created=%v err=%v", first, created, err)
	}
	if first.URLNormalized != "https://example.com/path?a=1&b=2" || !first.DateAdded.Equal(date) || !slices.Equal(first.TagNames, []string{"Beta", "Go-lang"}) {
		t.Fatalf("first bookmark: %+v", first)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET is_archived = 1 WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, created, err := r.CreateOrUpdateData(ctx, alice.ID, CreateInput{
		URL: "https://example.com/path?a=1&b=2", Title: "Second", Notes: "updated",
		TagNames: []string{"go-lang", "new"}, DateAdded: &date,
	})
	if err != nil || created {
		t.Fatalf("duplicate create: bookmark=%+v created=%v err=%v", second, created, err)
	}
	if second.ID != first.ID || second.URL != first.URL || second.Title != "Second" || second.Notes != "updated" ||
		second.Unread || second.Shared || !second.IsArchived || !second.DateAdded.Equal(date) || !slices.Equal(second.TagNames, []string{"Go-lang", "new"}) {
		t.Fatalf("duplicate merge mismatch: %+v", second)
	}
	if _, err := r.GetByID(ctx, bob.ID, first.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other owner accessed bookmark: %v", err)
	}
	bobsBookmark, created, err := r.CreateOrUpdateData(ctx, bob.ID, CreateInput{URL: second.URL})
	if err != nil || !created || bobsBookmark.ID == first.ID {
		t.Fatalf("same URL for another owner: bookmark=%+v created=%v err=%v", bobsBookmark, created, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmark`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("bookmark count=%d err=%v", count, err)
	}
	active, total, err := r.ListPage(ctx, alice.ID, false, 100, 0)
	if err != nil || total != 0 || len(active) != 0 {
		t.Fatalf("alice active: %v, count %d, err %v", active, total, err)
	}
	archived, total, err := r.ListPage(ctx, alice.ID, true, 100, 0)
	if err != nil || total != 1 || len(archived) != 1 || archived[0].ID != first.ID {
		t.Fatalf("alice archive: %v, count %d, err %v", archived, total, err)
	}
	active, total, err = r.ListPage(ctx, bob.ID, false, 100, 0)
	if err != nil || total != 1 || len(active) != 1 || active[0].ID != bobsBookmark.ID {
		t.Fatalf("bob active: %v, count %d, err %v", active, total, err)
	}
}

func TestCreateDuplicatePostgres(t *testing.T) {
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
		Username: fmt.Sprintf("bookmark_parity_%d", time.Now().UnixNano()), Password: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		queries := []string{
			`DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id = $1)`,
			`DELETE FROM bookmarks_bookmark WHERE owner_id = $1`,
			`DELETE FROM bookmarks_tag WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		}
		for _, query := range queries {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("cleanup PostgreSQL fixture: %v", err)
			}
		}
	})
	r := NewRepository(db, "postgres")
	first, created, err := r.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "HTTPS://Example.COM/", TagNames: []string{"Go", "go"}})
	if err != nil || !created || !slices.Equal(first.TagNames, []string{"go"}) {
		t.Fatalf("PostgreSQL create: bookmark=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := r.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com", Title: "Updated", TagNames: []string{"New"}})
	if err != nil || created || second.ID != first.ID || second.Title != "Updated" || !slices.Equal(second.TagNames, []string{"New"}) {
		t.Fatalf("PostgreSQL duplicate: bookmark=%+v created=%v err=%v", second, created, err)
	}
}

func TestBookmarkSaveAppliesAutoTagRules(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "autotag", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET auto_tagging_rules = ? WHERE user_id = ?`,
		"example.com/page auto\nother.example.com other", user.ID); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, "sqlite")
	first, created, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/page", TagNames: []string{"manual"}})
	if err != nil || !created || !slices.Equal(first.TagNames, []string{"auto", "manual"}) {
		t.Fatalf("first auto-tagging: %+v created=%t err=%v", first, created, err)
	}
	second, created, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "HTTPS://EXAMPLE.COM/page/", TagNames: []string{"manual2"}})
	if err != nil || created || second.ID != first.ID || !slices.Equal(second.TagNames, []string{"auto", "manual2"}) {
		t.Fatalf("duplicate auto-tagging: %+v created=%t err=%v", second, created, err)
	}
}

func TestUpdateDataPreservesOmittedFieldsAndChecksNormalizedDuplicate(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "edit-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "edit-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, "sqlite")
	first, _, err := repo.CreateOrUpdateData(ctx, alice.ID, CreateInput{URL: "https://example.com/first", Title: "First", TagNames: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.CreateOrUpdateData(ctx, alice.ID, CreateInput{URL: "https://example.com/second"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Edited"
	updated, err := repo.UpdateData(ctx, alice.ID, first.ID, UpdateInput{Title: &title})
	if err != nil || updated.Title != title || updated.URL != first.URL || !slices.Equal(updated.TagNames, first.TagNames) || !updated.DateModified.After(first.DateModified) {
		t.Fatalf("partial update: %+v, err=%v", updated, err)
	}
	duplicate := second.URL
	if _, err := repo.UpdateData(ctx, alice.ID, first.ID, UpdateInput{URL: &duplicate}); !errors.Is(err, ErrDuplicateURL) {
		t.Fatalf("exact duplicate error: %v", err)
	}
	if _, err := repo.UpdateData(ctx, bob.ID, first.ID, UpdateInput{Title: &title}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other owner update: %v", err)
	}
	variant := "HTTPS://EXAMPLE.COM/second/"
	if _, err := repo.UpdateData(ctx, alice.ID, first.ID, UpdateInput{URL: &variant}); !errors.Is(err, ErrDuplicateURL) {
		t.Fatalf("normalized duplicate edit: %v", err)
	}
}
