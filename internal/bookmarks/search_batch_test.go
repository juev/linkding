package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestLoadPageBookmarksPreservesFieldsOrderAndOwners(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	first, err := users.CreateUser(ctx, auth.NewUser{Username: "first", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := users.CreateUser(ctx, auth.NewUser{Username: "second", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, "sqlite")
	inputs := []struct {
		owner int64
		input CreateInput
	}{
		{first.ID, CreateInput{URL: "https://example.com/first", Title: "First", Description: "one", Notes: "note", Unread: true, Shared: true, TagNames: []string{"zeta", "alpha"}}},
		{second.ID, CreateInput{URL: "https://example.com/second", Title: "Second", Description: "two", Shared: true}},
		{first.ID, CreateInput{URL: "https://example.com/third", Title: "Third", IsArchived: true, TagNames: []string{"beta"}}},
	}
	refs := make([]bookmarkRef, 0, len(inputs))
	for _, fixture := range inputs {
		item, _, err := repo.CreateOrUpdateData(ctx, fixture.owner, fixture.input)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, bookmarkRef{id: item.ID, ownerID: fixture.owner})
	}
	refs[0], refs[2] = refs[2], refs[0]
	items, err := repo.loadPageBookmarks(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i, ref := range refs {
		want, err := repo.GetByID(ctx, ref.ownerID, ref.id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(items[i], want) {
			t.Fatalf("item %d differs from individual read: got %+v, want %+v", i, items[i], want)
		}
	}
	if items[1].TagNames != nil {
		t.Fatalf("empty tag list changed representation: %#v", items[1].TagNames)
	}
	if _, err := repo.loadPageBookmarks(ctx, []bookmarkRef{{id: refs[0].id, ownerID: second.ID}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong owner: got %v, want sql.ErrNoRows", err)
	}
	if _, err := repo.loadPageBookmarks(ctx, []bookmarkRef{{id: 999999, ownerID: first.ID}}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing bookmark: got %v, want sql.ErrNoRows", err)
	}
	if empty, err := repo.loadPageBookmarks(ctx, nil); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty page: items=%v err=%v", empty, err)
	}
}

func TestListFilteredLargePageUsesBatches(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "largepage", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 501 {
		url := fmt.Sprintf("https://example.com/page/%03d", i)
		_, err := tx.ExecContext(ctx, `INSERT INTO bookmarks_bookmark
			(url, url_normalized, title, description, notes, unread, shared, is_archived,
			 date_added, date_modified, owner_id, web_archive_snapshot_url, favicon_file, preview_image_file)
			VALUES (?, ?, ?, '', '', 0, 0, 0, ?, ?, ?, '', '', '')`, url, url, fmt.Sprintf("Item %03d", i), now.Add(time.Duration(i)*time.Second), now, user.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	items, count, err := NewRepository(db, "sqlite").ListFiltered(ctx, user.ID, ListOptions{Limit: 501})
	if err != nil {
		t.Fatal(err)
	}
	if count != 501 || len(items) != 501 {
		t.Fatalf("large page: count=%d length=%d", count, len(items))
	}
	if items[0].Title != "Item 500" || items[500].Title != "Item 000" {
		t.Fatalf("large page order: first=%q last=%q", items[0].Title, items[500].Title)
	}
}
