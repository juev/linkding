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
}
