package httpserver

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminLoginRequiresStaffAndPreservesNext(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/", SessionCookieAge: 3600}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	if _, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "admin-pass", IsStaff: true, IsSuperuser: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateUser(ctx, auth.NewUser{Username: "member", Password: "member-pass"}); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	adminPath := "/linkding/admin/"
	loginPath := adminPath + "login/"
	if got := get(adminPath + "tasks/?p=2"); got.Code != http.StatusFound || got.Header().Get("Location") != loginPath+"?next=/linkding/admin/tasks/%3Fp%3D2" {
		t.Fatalf("anonymous admin redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	member, err := users.AuthenticatePassword(ctx, "member", "member-pass")
	if err != nil {
		t.Fatal(err)
	}
	memberKey, err := users.CreateSession(ctx, member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	memberCookie := &http.Cookie{Name: auth.SessionCookieName, Value: memberKey}
	if got := get(adminPath, memberCookie); got.Code != http.StatusFound || got.Header().Get("Location") != loginPath+"?next=/linkding/admin/" {
		t.Fatalf("non-staff admin redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	if got := get(loginPath, memberCookie); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "You are authenticated as member, but are not authorized to access this page.") {
		t.Fatalf("non-staff admin login form: %d %s", got.Code, got.Body.String())
	}
	formPath := loginPath + "?next=/linkding/admin/bookmarks/bookmark/"
	formPage := get(formPath)
	if formPage.Code != http.StatusOK || !strings.Contains(formPage.Body.String(), "<title>Log in | linkding Admin</title>") || !strings.Contains(formPage.Body.String(), `action="/linkding/admin/login/?next=/linkding/admin/bookmarks/bookmark/"`) || !strings.Contains(formPage.Body.String(), `name="next" value="/linkding/admin/bookmarks/bookmark/"`) || !strings.Contains(formPage.Body.String(), `static/admin/css/login.css`) {
		t.Fatalf("admin login form: %d %s", formPage.Code, formPage.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, cookie := range formPage.Result().Cookies() {
		if cookie.Name == auth.CSRFCookieName {
			csrfCookie = cookie
		}
	}
	tokenMatch := regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`).FindStringSubmatch(formPage.Body.String())
	if csrfCookie == nil || csrfCookie.Path != "/linkding/" || len(tokenMatch) != 2 || !auth.VerifyCSRF(csrfCookie.Value, tokenMatch[1]) {
		t.Fatalf("admin CSRF form/cookie: cookie=%v match=%v", csrfCookie, tokenMatch)
	}
	post := func(username, password, next string, withCSRF bool) *httptest.ResponseRecorder {
		form := url.Values{"username": {username}, "password": {password}, "next": {next}, "csrfmiddlewaretoken": {tokenMatch[1]}}
		request := httptest.NewRequest(http.MethodPost, formPath, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCSRF {
			request.AddCookie(csrfCookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if got := post("admin", "admin-pass", adminPath, false); got.Code != http.StatusForbidden {
		t.Fatalf("admin login without CSRF: %d", got.Code)
	}
	for _, credentials := range [][2]string{{"admin", "wrong"}, {"member", "member-pass"}} {
		got := post(credentials[0], credentials[1], adminPath, true)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "<title>Error: Log in | linkding Admin</title>") || !strings.Contains(got.Body.String(), "Please enter the correct username and password for a staff account.") {
			t.Fatalf("rejected admin login %q: %d %s", credentials[0], got.Code, got.Body.String())
		}
		for _, cookie := range got.Result().Cookies() {
			if cookie.Name == auth.SessionCookieName {
				t.Fatalf("rejected admin login created a session for %q", credentials[0])
			}
		}
	}
	var memberLastLogin sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT last_login FROM auth_user WHERE username='member'`).Scan(&memberLastLogin); err != nil || memberLastLogin.Valid {
		t.Fatalf("non-staff login changed last_login: %v %v", memberLastLogin, err)
	}
	started := time.Now().UTC()
	success := post("admin", "admin-pass", "/linkding/admin/bookmarks/bookmark/", true)
	if success.Code != http.StatusFound || success.Header().Get("Location") != "/linkding/admin/bookmarks/bookmark/" {
		t.Fatalf("staff login redirect: %d %q", success.Code, success.Header().Get("Location"))
	}
	var sessionCookie *http.Cookie
	for _, cookie := range success.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || sessionCookie.Path != "/linkding/" || !sessionCookie.HttpOnly {
		t.Fatalf("staff session cookie: %v", sessionCookie)
	}
	var staffLastLogin sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT last_login FROM auth_user WHERE username='admin'`).Scan(&staffLastLogin); err != nil || !staffLastLogin.Valid || staffLastLogin.Time.Before(started.Add(-time.Second)) {
		t.Fatalf("staff login did not update last_login: %v %v", staffLastLogin, err)
	}
	if got := get(adminPath, sessionCookie); got.Code != http.StatusOK {
		t.Fatalf("staff dashboard after login: %d", got.Code)
	}
	if got := get(formPath, sessionCookie); got.Code != http.StatusFound || got.Header().Get("Location") != adminPath {
		t.Fatalf("authenticated admin login redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	if got := post("admin", "admin-pass", "https://evil.example/steal", true); got.Code != http.StatusFound || got.Header().Get("Location") != "/linkding/bookmarks" {
		t.Fatalf("unsafe admin next: %d %q", got.Code, got.Header().Get("Location"))
	}
}
