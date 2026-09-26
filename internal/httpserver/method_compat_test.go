package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPinnedUIReadMethods(t *testing.T) {
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
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	check := func(method, path string, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		if signedIn {
			request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"/bookmarks", "/bookmarks/new", "/tags/new", "/bundles/new", "/settings/general", "/admin/tasks/", "/admin/auth/user/", "/feeds/shared", "/health"} {
		response := check(http.MethodOptions, path, true)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Errorf("OPTIONS %s: status %d, body size %d", path, response.Code, response.Body.Len())
		}
	}
	for _, path := range []string{"/bookmarks/new", "/tags/new", "/bundles/new", "/bundles/preview", "/change-password/"} {
		if response := check(http.MethodHead, path, true); response.Code != http.StatusOK {
			t.Errorf("HEAD %s: status %d", path, response.Code)
		}
	}
	for path, destination := range map[string]string{
		"/bookmarks/action":          "/bookmarks",
		"/bookmarks/archived/action": "/bookmarks/archived",
		"/bookmarks/shared/action":   "/bookmarks/shared",
		"/bundles/action":            "/bundles",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			response := check(method, path, true)
			if response.Code != http.StatusFound || response.Header().Get("Location") != destination {
				t.Errorf("%s %s: status %d, redirect %q", method, path, response.Code, response.Header().Get("Location"))
			}
		}
	}
	for path, allow := range map[string]string{
		"/logout/":               "POST, OPTIONS",
		"/change-password/":      "GET, POST, PUT, HEAD, OPTIONS",
		"/password-change-done/": "GET, HEAD, OPTIONS",
	} {
		response := check(http.MethodOptions, path, true)
		if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Allow") != allow {
			t.Errorf("OPTIONS %s: status %d, body size %d, Allow %q", path, response.Code, response.Body.Len(), response.Header().Get("Allow"))
		}
	}
	if response := check(http.MethodOptions, "/login/", true); response.Code != http.StatusFound || response.Header().Get("Location") != "/bookmarks" {
		t.Errorf("authenticated OPTIONS login: %d %q", response.Code, response.Header().Get("Location"))
	}
	if response := check(http.MethodOptions, "/login/", false); response.Code != http.StatusOK || response.Body.Len() != 0 || !strings.Contains(response.Header().Get("Allow"), "PUT") {
		t.Errorf("guest OPTIONS login: %d, body size %d, Allow %q", response.Code, response.Body.Len(), response.Header().Get("Allow"))
	}
	cfg.ContextPath = "linkding/"
	prefixed := New(db, cfg, t.TempDir())
	for _, path := range []string{"/linkding/bookmarks", "/linkding/bookmarks/new"} {
		request := httptest.NewRequest(http.MethodOptions, path, nil)
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		response := httptest.NewRecorder()
		prefixed.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("context path OPTIONS %s: status %d", path, response.Code)
		}
	}
}
