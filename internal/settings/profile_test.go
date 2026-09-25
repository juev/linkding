package settings

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestProfileFormUpdatesPinnedFieldsAndHash(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "settings", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	form, err := LoadProfileForm(ctx, db, "sqlite", user.ID)
	if err != nil || form.Get("theme") != "auto" || form.Get("items_per_page") != "30" || form.Get("display_view_bookmark_action") != "on" {
		t.Fatalf("default form: %v err=%v", form, err)
	}
	form.Set("theme", "dark")
	form.Set("items_per_page", "10")
	form.Set("custom_css", "body { color: red; }")
	form.Set("auto_tagging_rules", "example.com tag")
	form.Set("enable_favicons", "on")
	form.Set("display_view_bookmark_action", "False")
	form.Del("enable_automatic_html_snapshots")
	if err := UpdateProfile(ctx, db, "sqlite", user.ID, form); err != nil {
		t.Fatal(err)
	}
	updated, err := LoadProfileForm(ctx, db, "sqlite", user.ID)
	if err != nil || updated.Get("theme") != "dark" || updated.Get("items_per_page") != "10" || updated.Get("enable_favicons") != "on" ||
		updated.Get("display_view_bookmark_action") != "" || updated.Get("enable_automatic_html_snapshots") != "" {
		t.Fatalf("updated form: %v err=%v", updated, err)
	}
	var hash, prefs string
	if err := db.QueryRowContext(ctx, `SELECT custom_css_hash, search_preferences FROM bookmarks_userprofile WHERE user_id=?`, user.ID).Scan(&hash, &prefs); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("body { color: red; }"))
	if hash != hex.EncodeToString(sum[:]) || prefs != "{}" {
		t.Fatalf("hash=%s search preferences=%s", hash, prefs)
	}
	form.Set("items_per_page", "-1")
	if err := UpdateProfile(ctx, db, "sqlite", user.ID, form); err == nil || !errors.As(err, new(ValidationError)) {
		t.Fatalf("invalid page size: %v", err)
	}
	form.Set("items_per_page", "30")
	form.Set("custom_css", "")
	if err := UpdateProfile(ctx, db, "sqlite", user.ID, form); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id=?`, user.ID).Scan(&hash); err != nil || hash != "" {
		t.Fatalf("cleared custom CSS hash: %q err=%v", hash, err)
	}
	form.Del("enable_favicons")
	if err := UpdateProfile(ctx, db, "sqlite", user.ID, form); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	form.Set("enable_favicons", "on")
	for range 2 {
		if err := UpdateProfileWithTasks(ctx, db, "sqlite", user.ID, form, false); err != nil {
			t.Fatal(err)
		}
	}
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM linkding_job WHERE kind='load_favicon'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("favicon scheduling on enable: queued=%d err=%v", queued, err)
	}
}

func TestProfileFormPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to disposable PostgreSQL")
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
		Username: fmt.Sprintf("settings_%d", time.Now().UnixNano()), Password: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_userprofile WHERE user_id=$1`, user.ID); err != nil {
			t.Error(err)
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM auth_user WHERE id=$1`, user.ID); err != nil {
			t.Error(err)
		}
	})
	form, err := LoadProfileForm(ctx, db, "postgres", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	form.Set("theme", "dark")
	form.Set("custom_css", "body{}")
	if err := UpdateProfile(ctx, db, "postgres", user.ID, form); err != nil {
		t.Fatal(err)
	}
	updated, err := LoadProfileForm(ctx, db, "postgres", user.ID)
	if err != nil || updated.Get("theme") != "dark" || updated.Get("custom_css") != "body{}" {
		t.Fatalf("PostgreSQL form: %v err=%v", updated, err)
	}
}
