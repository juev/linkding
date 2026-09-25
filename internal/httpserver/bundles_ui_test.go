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

func TestBundlesUIEditorPreviewMoveAndOwner(t *testing.T) {
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
	repo := bookmarks.NewRepository(db, "sqlite")
	if _, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://one.example", Title: "One", TagNames: []string{"coding"}, Unread: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://two.example", Title: "Two"}); err != nil {
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
	request := func(method, path string, form url.Values, authorized bool) *httptest.ResponseRecorder {
		var r *http.Request
		if form == nil {
			r = httptest.NewRequest(method, path, nil)
		} else {
			if authorized {
				form.Set("csrfmiddlewaretoken", csrf)
			}
			r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		if authorized {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/bundles", nil, true); got.Code != 200 || !strings.Contains(got.Body.String(), "You have no bundles yet") {
		t.Fatalf("empty index: %d", got.Code)
	}
	if got := request(http.MethodGet, "/bundles/new", nil, true); got.Code != 200 || !strings.Contains(got.Body.String(), "Found 2 bookmarks matching this bundle.") {
		t.Fatalf("new preview: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "/bundles/preview?all_tags=coding", nil, true); got.Code != 200 || !strings.Contains(got.Body.String(), "Found 1 bookmarks matching this bundle.") || strings.Contains(got.Body.String(), "Two") {
		t.Fatalf("filtered preview: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/bundles/new", url.Values{"name": {"Go"}, "all_tags": {"coding"}, "filter_unread": {"yes"}, "filter_shared": {"off"}}, false); got.Code != 403 {
		t.Fatalf("missing CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, "/bundles/new", url.Values{"name": {"Go"}, "all_tags": {"coding"}, "filter_unread": {"yes"}, "filter_shared": {"off"}}, true); got.Code != 302 {
		t.Fatalf("create: %d %q", got.Code, got.Body.String())
	}
	var firstID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkbundle WHERE owner_id=? AND name='Go'`, alice.ID).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/bundles/new", url.Values{"name": {"Other"}, "filter_unread": {"off"}, "filter_shared": {"off"}}, true); got.Code != 302 {
		t.Fatalf("create second: %d", got.Code)
	}
	var secondID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkbundle WHERE owner_id=? AND name='Other'`, alice.ID).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodPost, "/bundles/action", url.Values{"move_bundle": {strconv.FormatInt(secondID, 10)}, "move_position": {"0"}}, true); got.Code != 302 {
		t.Fatalf("move: %d %q", got.Code, got.Body.String())
	}
	var firstOrder, secondOrder int
	if err := db.QueryRowContext(ctx, `SELECT "order" FROM bookmarks_bookmarkbundle WHERE id=?`, firstID).Scan(&firstOrder); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT "order" FROM bookmarks_bookmarkbundle WHERE id=?`, secondID).Scan(&secondOrder); err != nil {
		t.Fatal(err)
	}
	if firstOrder != 1 || secondOrder != 0 {
		t.Fatalf("order after move: %d %d", firstOrder, secondOrder)
	}
	if got := request(http.MethodGet, "/bundles/"+strconv.FormatInt(firstID, 10)+"/edit", nil, true); got.Code != 200 || !strings.Contains(got.Body.String(), `value="Go"`) {
		t.Fatalf("edit form: %d", got.Code)
	}
	if got := request(http.MethodPost, "/bundles/"+strconv.FormatInt(firstID, 10)+"/edit", url.Values{"name": {"Go updated"}, "all_tags": {"coding"}, "filter_unread": {"yes"}, "filter_shared": {"off"}}, true); got.Code != 302 {
		t.Fatalf("edit: %d %q", got.Code, got.Body.String())
	}
	listed, count, err := repo.ListFiltered(ctx, alice.ID, bookmarks.ListOptions{Bundle: strconv.FormatInt(firstID, 10), Limit: 30})
	if err != nil || count != 1 || len(listed) != 1 || listed[0].Title != "One" {
		t.Fatalf("saved bundle filter: %d %v %v", count, listed, err)
	}
	foreign, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,filter_unread,filter_shared,"order",date_created,date_modified,owner_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "Foreign", "", "", "", "", "off", "off", 0, time.Now(), time.Now(), bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreignID, err := foreign.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/bundles/"+strconv.FormatInt(foreignID, 10)+"/edit", nil, true); got.Code != 404 {
		t.Fatalf("foreign edit: %d", got.Code)
	}
	if got := request(http.MethodPost, "/bundles/action", url.Values{"remove_bundle": {strconv.FormatInt(foreignID, 10)}}, true); got.Code != 404 {
		t.Fatalf("foreign delete: %d", got.Code)
	}
	if got := request(http.MethodPost, "/bundles/action", url.Values{"remove_bundle": {strconv.FormatInt(secondID, 10)}}, true); got.Code != 302 {
		t.Fatalf("delete: %d", got.Code)
	}
	if err := db.QueryRowContext(ctx, `SELECT "order" FROM bookmarks_bookmarkbundle WHERE id=?`, firstID).Scan(&firstOrder); err != nil || firstOrder != 0 {
		t.Fatalf("order after delete: %d %v", firstOrder, err)
	}
}
