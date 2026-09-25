package httpserver

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminFeedTokenCRUDAndReservedKey(t *testing.T) {
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
	adminSession, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	requestAs := func(method, path, session string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		if withCSRF {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	request := func(method, path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		return requestAs(method, path, adminSession, form, withCSRF)
	}
	base := "/admin/bookmarks/feedtoken/"
	if got := request(http.MethodGet, base+"add/", nil, false); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `name="key"`) || !strings.Contains(got.Body.String(), `name="user"`) {
		t.Fatalf("add form: %d %s", got.Code, got.Body.String())
	}
	form := url.Values{"key": {" initial "}, "user": {strconv.FormatInt(alice.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", form, false); got.Code != http.StatusForbidden {
		t.Fatalf("add without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, base+"add/", form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base {
		t.Fatalf("add token: %d %q %s", got.Code, got.Header().Get("Location"), got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", url.Values{"key": {"second"}, "user": {strconv.FormatInt(alice.ID, 10)}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "already has a feed token") {
		t.Fatalf("duplicate user validation: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", url.Values{"key": {"initial"}, "user": {strconv.FormatInt(bob.ID, 10)}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "key already exists") {
		t.Fatalf("duplicate key validation: %d %s", got.Code, got.Body.String())
	}

	reservedKey := "odd/a%?&key"
	change := base + url.PathEscape("initial") + "/change/"
	unchangedForm := url.Values{"key": {"initial"}, "user": {strconv.FormatInt(alice.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, change, unchangedForm, true); got.Code != http.StatusFound {
		t.Fatalf("save unchanged key: %d %s", got.Code, got.Body.String())
	}
	changeForm := url.Values{"key": {reservedKey}, "user": {strconv.FormatInt(alice.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, change, changeForm, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "already has a feed token") {
		t.Fatalf("changed key with existing user: %d %s", got.Code, got.Body.String())
	}
	changeForm.Set("user", strconv.FormatInt(bob.ID, 10))
	if got := request(http.MethodPost, change, changeForm, true); got.Code != http.StatusFound {
		t.Fatalf("change key: %d %s", got.Code, got.Body.String())
	}
	var storedUser int64
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM bookmarks_feedtoken WHERE key = ?`, reservedKey).Scan(&storedUser); err != nil || storedUser != bob.ID {
		t.Fatalf("new token user: %d %v", storedUser, err)
	}
	var oldUser int64
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM bookmarks_feedtoken WHERE key = ?`, "initial").Scan(&oldUser); err != nil || oldUser != alice.ID {
		t.Fatalf("original token was not preserved: %d %v", oldUser, err)
	}
	var tokenCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_feedtoken`).Scan(&tokenCount); err != nil || tokenCount != 2 {
		t.Fatalf("expected original and new token, got %d: %v", tokenCount, err)
	}
	list := request(http.MethodGet, base, nil, false)
	wantLink := `href="` + html.EscapeString(base+url.PathEscape(reservedKey)+"/change/") + `"`
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), wantLink) {
		t.Fatalf("list link for escaped key %q: %d %s", wantLink, list.Code, list.Body.String())
	}
	if got := request(http.MethodGet, base+url.PathEscape(reservedKey)+"/change/", nil, false); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `value="`+html.EscapeString(reservedKey)+`"`) {
		t.Fatalf("follow escaped list link: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, base+url.PathEscape("missing")+"/change/", nil, false); got.Code != http.StatusNotFound {
		t.Fatalf("missing token: %d", got.Code)
	}
	if got := request(http.MethodPost, base+url.PathEscape(reservedKey)+"/delete/", url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != http.StatusFound {
		t.Fatalf("delete token: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_feedtoken`).Scan(&tokenCount); err != nil || tokenCount != 1 {
		t.Fatalf("deleting new token should preserve original, got %d: %v", tokenCount, err)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "feedtoken", "view_feedtoken")
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestAs(http.MethodGet, change, viewerSession, nil, false); got.Code != http.StatusOK || strings.Contains(got.Body.String(), `value="Save"`) {
		t.Fatalf("view-only form: %d %s", got.Code, got.Body.String())
	}
	if got := requestAs(http.MethodPost, change, viewerSession, unchangedForm, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only change: %d", got.Code)
	}
	if got := requestAs(http.MethodGet, base+"add/", viewerSession, nil, false); got.Code != http.StatusForbidden {
		t.Fatalf("view-only add: %d", got.Code)
	}
	aliceSession, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestAs(http.MethodGet, change, aliceSession, nil, false); got.Code != http.StatusForbidden {
		t.Fatalf("non-staff change: %d", got.Code)
	}
}
