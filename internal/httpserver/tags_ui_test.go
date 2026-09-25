package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestTagsUIOwnerCreateRenameMergeDelete(t *testing.T) {
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
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		var r *http.Request
		if form == nil {
			r = httptest.NewRequest(method, path, nil)
		} else {
			form.Set("csrfmiddlewaretoken", csrf)
			r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/tags", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "You have no tags yet") {
		t.Fatalf("empty index: %d", got.Code)
	}
	if got := request(http.MethodGet, "/tags/new", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `id="tag-modal"`) {
		t.Fatalf("new modal: %d", got.Code)
	}
	if got := request(http.MethodPost, "/tags/new", url.Values{"name": {"hello world"}}); got.Code != 302 {
		t.Fatalf("create: %d %q", got.Code, got.Body.String())
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_tag WHERE owner_id=? AND name='hello-world'`, alice.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/tags/new", url.Values{"name": {"HELLO WORLD"}}); got.Code != 200 || !strings.Contains(got.Body.String(), "already exists") {
		t.Fatalf("duplicate: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/tags/"+strconv.FormatInt(id, 10)+"/edit", url.Values{"name": {"renamed"}}); got.Code != 302 {
		t.Fatalf("edit: %d %q", got.Code, got.Body.String())
	}
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM bookmarks_tag WHERE id=?`, id).Scan(&name); err != nil || name != "renamed" {
		t.Fatalf("renamed: %q %v", name, err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	bookmark, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://tags.example", TagNames: []string{"renamed", "source"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateOrUpdateData(ctx, bob.ID, bookmarks.CreateInput{URL: "https://bob.example", TagNames: []string{"foreign"}}); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/tags?sort=count-desc", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "renamed") || !strings.Contains(got.Body.String(), "source") {
		t.Fatalf("populated index: %d", got.Code)
	}
	if got := request(http.MethodPost, "/tags/merge", url.Values{"target_tag": {"renamed"}, "merge_tags": {"source"}}); got.Code != 302 {
		t.Fatalf("merge: %d %q", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_tag WHERE owner_id=? AND name='source'`, alice.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("source deleted: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=? AND tag_id=?`, bookmark.ID, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("target relationship: %d %v", count, err)
	}
	var foreignID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_tag WHERE owner_id=?`, bob.ID).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/tags", url.Values{"delete_tag": {strconv.FormatInt(foreignID, 10)}}); got.Code != 404 {
		t.Fatalf("foreign deletion: %d", got.Code)
	}
	if got := request(http.MethodPost, "/tags", url.Values{"delete_tag": {strconv.FormatInt(id, 10)}}); got.Code != 302 {
		t.Fatalf("delete: %d", got.Code)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=?`, bookmark.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("relationships after deletion: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_tag WHERE id=?`, foreignID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign tag changed: %d %v", count, err)
	}
}
