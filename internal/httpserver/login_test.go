package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestLoginSessionProfileAndLogoutWithContextPath(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/", SessionCookieAge: 3600}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "alice", Password: "correct"}); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	loginPath := "/linkding/login/"
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, loginPath, nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `action="/linkding/login/"`) || !strings.Contains(get.Body.String(), `theme-light.css?v=1.47.0`) {
		t.Fatalf("login GET: status=%d body=%s", get.Code, get.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range get.Result().Cookies() {
		if cookie.Name == auth.CSRFCookieName {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil || csrfCookie.Path != "/linkding/" || csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("CSRF cookie: %+v", csrfCookie)
	}
	match := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if len(match) != 2 || !auth.VerifyCSRF(csrfCookie.Value, match[1]) {
		t.Fatal("form CSRF token does not match cookie")
	}
	postForm := func(password, next string, withCSRF bool) *httptest.ResponseRecorder {
		form := url.Values{"username": {"alice"}, "password": {password}, "next": {next}, "csrfmiddlewaretoken": {match[1]}}
		req := httptest.NewRequest(http.MethodPost, loginPath, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCSRF {
			req.AddCookie(csrfCookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := postForm("correct", "", false); response.Code != http.StatusForbidden {
		t.Fatalf("login without CSRF: %d", response.Code)
	}
	if response := postForm("wrong", "", true); response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "didn't match") {
		t.Fatalf("wrong login: status=%d body=%s", response.Code, response.Body.String())
	}
	success := postForm("correct", "https://evil.example/steal", true)
	if success.Code != http.StatusFound || success.Header().Get("Location") != "/linkding/bookmarks" {
		t.Fatalf("login redirect: status=%d location=%q", success.Code, success.Header().Get("Location"))
	}
	var sessionCookie, newCSRF *http.Cookie
	for _, cookie := range success.Result().Cookies() {
		switch cookie.Name {
		case auth.SessionCookieName:
			sessionCookie = cookie
		case auth.CSRFCookieName:
			newCSRF = cookie
		}
	}
	if sessionCookie == nil || sessionCookie.Path != "/linkding/" || !sessionCookie.HttpOnly || sessionCookie.MaxAge != 3600 || newCSRF == nil {
		t.Fatalf("login cookies: session=%+v csrf=%+v", sessionCookie, newCSRF)
	}
	profile := httptest.NewRecorder()
	profileReq := httptest.NewRequest(http.MethodGet, "/linkding/api/user/profile/", nil)
	profileReq.AddCookie(sessionCookie)
	handler.ServeHTTP(profile, profileReq)
	if profile.Code != http.StatusOK {
		t.Fatalf("session profile: status=%d body=%s", profile.Code, profile.Body.String())
	}
	logoutToken, err := auth.MaskCSRF(newCSRF.Value)
	if err != nil {
		t.Fatal(err)
	}
	logoutForm := url.Values{"csrfmiddlewaretoken": {logoutToken}}
	logoutReq := httptest.NewRequest(http.MethodPost, "/linkding/logout/", strings.NewReader(logoutForm.Encode()))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutReq.AddCookie(sessionCookie)
	logoutReq.AddCookie(newCSRF)
	logout := httptest.NewRecorder()
	handler.ServeHTTP(logout, logoutReq)
	if logout.Code != http.StatusFound || logout.Header().Get("Location") != "/linkding/login/" {
		t.Fatalf("logout: status=%d location=%q", logout.Code, logout.Header().Get("Location"))
	}
	if _, err := auth.NewRepository(db, "sqlite").AuthenticateSession(ctx, sessionCookie.Value); err == nil {
		t.Fatal("logout left session valid")
	}
}

func TestDisabledLoginFormKeepsOIDCLink(t *testing.T) {
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableLoginForm: true, EnableOIDC: true}
	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New(db, cfg, t.TempDir())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login/", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `<form method="post"`) || !strings.Contains(response.Body.String(), `data-turbo="false"`) {
		t.Fatalf("disabled login form: status=%d body=%s", response.Code, response.Body.String())
	}
}
