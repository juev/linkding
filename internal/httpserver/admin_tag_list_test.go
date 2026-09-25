package httpserver

import (
	"context"
	"html"
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

func TestAdminTagListSearchAndOwnerFilter(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), TimeZone: "Europe/Moscow"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, cfg.DBEngine)
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
	added := time.Date(2020, 2, 3, 1, 5, 0, 0, time.UTC)
	for _, tag := range []struct {
		name  string
		owner int64
	}{
		{"literal%tag", alice.ID},
		{"plain-tag", alice.ID},
		{"other%tag", bob.ID},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (?,?,?)`, tag.name, added, tag.owner); err != nil {
			t.Fatal(err)
		}
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/bookmarks/tag/?q=%25&owner__username=alice", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	New(db, cfg, t.TempDir()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tag list: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "literal%tag") || strings.Contains(body, "plain-tag") || strings.Contains(body, "other%tag") {
		t.Fatalf("search or owner filter selected wrong tags: %s", body)
	}
	if !strings.Contains(body, "Feb. 3, 2020, 4:05 a.m.") {
		t.Fatalf("tag date ignored configured timezone: %s", body)
	}
	if !strings.Contains(body, `name="q" value="%"`) || !strings.Contains(body, `name="owner__username" value="alice"`) {
		t.Fatalf("search and filter controls lost their values: %s", body)
	}
	filterURL := adminListURL(url.Values{"q": {"%"}, "owner__username": {"alice"}}, "owner__username", "bob")
	if !strings.Contains(body, html.EscapeString(filterURL)) {
		t.Fatalf("owner filter link lost search query: %s", body)
	}
	for _, tc := range []struct {
		order string
		names []string
	}{
		{"1", []string{"literal%tag", "other%tag", "plain-tag"}},
		{"-1", []string{"plain-tag", "other%tag", "literal%tag"}},
	} {
		r := httptest.NewRequest(http.MethodGet, "/admin/bookmarks/tag/?o="+tc.order, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		w := httptest.NewRecorder()
		New(db, cfg, t.TempDir()).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("sort %s: status=%d", tc.order, w.Code)
		}
		filterSuffix := "?_changelist_filters=" + url.QueryEscape("o="+tc.order)
		if !strings.Contains(w.Body.String(), `href="/admin/bookmarks/tag/add/`+filterSuffix+`"`) ||
			!strings.Contains(w.Body.String(), `/change/`+filterSuffix+`"`) ||
			!strings.Contains(w.Body.String(), `?_facets=True&amp;o=`+tc.order) {
			t.Fatalf("sort %s did not preserve changelist filters in links", tc.order)
		}
		result := w.Body.String()
		result = result[strings.Index(result, `<table id="result_list">`):]
		last := -1
		for _, name := range tc.names {
			position := strings.Index(result, ">"+name+"</a>")
			if position <= last {
				t.Fatalf("sort %s: %q appeared at %d after %d", tc.order, name, position, last)
			}
			last = position
		}
	}
}
