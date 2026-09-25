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
	"github.com/juev/linkding/internal/settings"
	"github.com/juev/linkding/internal/store"
)

func TestSettingsPageAndProfileUpdate(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true, EnableSnapshots: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "settings", Password: "password", IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path string, form url.Values) *http.Request {
		t.Helper()
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		return r
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, request(http.MethodGet, "/settings/general", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `class="settings-page"`) ||
		!strings.Contains(page.Body.String(), `name="theme"`) || !strings.Contains(page.Body.String(), `name="update_global_settings"`) ||
		!strings.Contains(page.Body.String(), `action="/settings/import"`) || !strings.Contains(page.Body.String(), `name="csrfmiddlewaretoken"`) {
		t.Fatalf("settings page: status=%d body=%q", page.Code, page.Body.String())
	}
	form, err := settings.LoadProfileForm(ctx, db, "sqlite", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	form.Set("theme", "dark")
	form.Set("custom_css", "body { color: red; }")
	form.Set("update_profile", "Save")
	form.Set("csrfmiddlewaretoken", csrf)
	denied := httptest.NewRecorder()
	bad := request(http.MethodPost, "/settings/update", form)
	bad.Header.Set("Origin", "http://evil.example")
	handler.ServeHTTP(denied, bad)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", denied.Code)
	}
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, request(http.MethodPost, "/settings/update", form))
	if updated.Code != http.StatusFound || updated.Header().Get("Location") != "/settings/general" {
		t.Fatalf("update: %d %q", updated.Code, updated.Header().Get("Location"))
	}
	var theme, cssHash string
	if err := db.QueryRowContext(ctx, `SELECT theme, custom_css_hash FROM bookmarks_userprofile WHERE user_id=?`, user.ID).Scan(&theme, &cssHash); err != nil || theme != "dark" || cssHash == "" {
		t.Fatalf("stored profile: %q %q %v", theme, cssHash, err)
	}
	flashPage := httptest.NewRecorder()
	flashReq := request(http.MethodGet, "/settings/general", nil)
	for _, cookie := range updated.Result().Cookies() {
		flashReq.AddCookie(cookie)
	}
	handler.ServeHTTP(flashPage, flashReq)
	if !strings.Contains(flashPage.Body.String(), "Profile updated") || !strings.Contains(flashPage.Body.String(), `theme-dark.css`) {
		t.Fatalf("updated page: %d %q", flashPage.Code, flashPage.Body.String())
	}
	form.Set("items_per_page", "1")
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, request(http.MethodPost, "/settings/update", form))
	if invalid.Code != 422 || !strings.Contains(invalid.Body.String(), "Profile update failed") {
		t.Fatalf("invalid update: %d %q", invalid.Code, invalid.Body.String())
	}
}
