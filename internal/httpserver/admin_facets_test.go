package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminBookmarkFacetCountsRespectSearchAndOtherFilters(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tag, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES ('Blue',?,?)`, now, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	tagID, _ := tag.LastInsertId()
	for _, item := range []struct {
		title    string
		owner    int64
		archived bool
		unread   bool
		tagged   bool
	}{
		{"Alpha one", alice.ID, false, false, true},
		{"Alpha two", alice.ID, true, true, true},
		{"Beta", bob.ID, false, true, false},
	} {
		result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES (?,?,?,?,?,NULL,NULL,?,?,0,?,?,?,'','','')`, "https://example.test/"+url.PathEscape(item.title), "https://example.test/"+url.PathEscape(item.title), item.title, "", "", item.unread, item.archived, now, now, item.owner)
		if err != nil {
			t.Fatal(err)
		}
		if item.tagged {
			id, _ := result.LastInsertId()
			if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) VALUES (?,?)`, id, tagID); err != nil {
				t.Fatal(err)
			}
		}
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(path string) string {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/admin/bookmarks/bookmark/"
	body := request(base + "?_facets=True&is_archived__exact=1")
	for _, label := range []string{"alice (1)", "bob (0)", "Yes (1)", "No (2)", "Blue (1)", "- (0)", "✖ Clear all filters"} {
		if !strings.Contains(body, label) {
			t.Errorf("filtered facets missing %q", label)
		}
	}
	if !strings.Contains(body, `href="?_facets=True" class="hidelink">✖ Clear all filters`) {
		t.Error("clear filters link did not retain facets")
	}
	body = request(base + "?_facets=True&is_archived__exact=1&q=Beta")
	for _, label := range []string{"alice (0)", "bob (0)", "Yes (0)", "No (1)", "Blue (0)", "- (0)"} {
		if !strings.Contains(body, label) {
			t.Errorf("search facets missing %q", label)
		}
	}
	if !strings.Contains(body, `href="?_facets=True&amp;q=Beta" class="hidelink">✖ Clear all filters`) {
		t.Error("clear filters link did not retain search")
	}
	if body := request(base); strings.Contains(body, "alice (2)") || strings.Contains(body, "Blue (2)") {
		t.Error("facet counts appeared without _facets=True")
	}
}
