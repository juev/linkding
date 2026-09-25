package jobs_test

import (
	"context"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/store"
)

func TestSettingsQueueRefreshAndMissingSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	repo := bookmarks.NewRepository(db, "sqlite")
	for _, item := range []struct {
		owner int64
		url   string
	}{{alice.ID, "https://one.example"}, {alice.ID, "https://two.example"}, {bob.ID, "https://other.example"}} {
		if _, _, err := repo.CreateOrUpdateData(ctx, item.owner, bookmarks.CreateInput{URL: item.url}); err != nil {
			t.Fatal(err)
		}
	}
	if err := jobs.EnqueueRefreshFavicons(ctx, db, "sqlite", alice.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM linkding_job WHERE kind='load_favicon'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled favicons: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET enable_favicons=1 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := jobs.EnqueueRefreshFavicons(ctx, db, "sqlite", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM linkding_job WHERE kind='load_favicon'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("refresh jobs: %d %v", count, err)
	}
	created, err := jobs.EnqueueMissingSnapshots(ctx, db, "sqlite", alice.ID)
	if err != nil || created != 2 {
		t.Fatalf("new snapshots: %d %v", created, err)
	}
	created, err = jobs.EnqueueMissingSnapshots(ctx, db, "sqlite", alice.ID)
	if err != nil || created != 0 {
		t.Fatalf("duplicate snapshots: %d %v", created, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmarkasset a JOIN bookmarks_bookmark b ON b.id=a.bookmark_id WHERE b.owner_id=? AND a.status='pending'`, alice.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("pending assets: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmarkasset SET status='failure' WHERE bookmark_id=(SELECT id FROM bookmarks_bookmark WHERE url='https://one.example')`); err != nil {
		t.Fatal(err)
	}
	created, err = jobs.EnqueueMissingSnapshots(ctx, db, "sqlite", alice.ID)
	if err != nil || created != 1 {
		t.Fatalf("retry failed snapshot: %d %v", created, err)
	}
}
