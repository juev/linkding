package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestIntegrationsTokenLifecycleAndFeed(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
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
	aliceSession, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bobSession, err := users.CreateSession(ctx, bob.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path, session string, form url.Values) *http.Request {
		t.Helper()
		var body strings.Reader
		if form != nil {
			body = *strings.NewReader(form.Encode())
		} else {
			body = *strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, &body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		return r
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, request("GET", "/settings/integrations", aliceSession, nil))
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="api-section"`) || !strings.Contains(page.Body.String(), `id="bookmarklet-server" href="javascript:`) || !strings.Contains(page.Body.String(), `feeds/`) {
		t.Fatalf("integrations page: %d %q", page.Code, page.Body.String())
	}
	var feedKey string
	if err := db.QueryRowContext(ctx, `SELECT key FROM bookmarks_feedtoken WHERE user_id=?`, alice.ID).Scan(&feedKey); err != nil || len(feedKey) != 40 {
		t.Fatalf("feed token: %q %v", feedKey, err)
	}
	form := url.Values{"name": {"Mobile App"}, "csrfmiddlewaretoken": {csrf}}
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request("POST", "/settings/integrations/create-api-token", aliceSession, form))
	if created.Code != 302 || created.Header().Get("Location") != "/settings/integrations" {
		t.Fatalf("create: %d %q", created.Code, created.Header().Get("Location"))
	}
	var tokenID int64
	var key string
	if err := db.QueryRowContext(ctx, `SELECT id,key FROM bookmarks_apitoken WHERE user_id=?`, alice.ID).Scan(&tokenID, &key); err != nil || len(key) != 40 {
		t.Fatalf("stored token: %d %q %v", tokenID, key, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(key) {
		t.Fatalf("invalid token key: %q", key)
	}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request("GET", "/settings/integrations", aliceSession, nil))
	if first.Code != 200 || !strings.Contains(first.Body.String(), key) || !strings.Contains(first.Body.String(), "Mobile App") {
		t.Fatalf("one-time token: %d %q", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request("GET", "/settings/integrations", aliceSession, nil))
	if second.Code != 200 || strings.Contains(second.Body.String(), key) {
		t.Fatalf("token shown twice: %d %q", second.Code, second.Body.String())
	}
	deleteForm := url.Values{"token_id": {strconv.FormatInt(tokenID, 10)}, "csrfmiddlewaretoken": {csrf}}
	other := httptest.NewRecorder()
	handler.ServeHTTP(other, request("POST", "/settings/integrations/delete-api-token", bobSession, deleteForm))
	if other.Code != 404 {
		t.Fatalf("other user's token deleted: %d", other.Code)
	}
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, request("POST", "/settings/integrations/delete-api-token", aliceSession, deleteForm))
	if deleted.Code != 302 {
		t.Fatalf("delete: %d", deleted.Code)
	}
	if _, err := users.AuthenticateToken(ctx, key); err != auth.ErrInvalidCredentials {
		t.Fatalf("deleted token remains valid: %v", err)
	}
}
