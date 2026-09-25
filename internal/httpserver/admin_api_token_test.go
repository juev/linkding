package httpserver

import (
	"context"
	"encoding/hex"
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

func TestAdminAPITokenCRUDSearchAndPermissions(t *testing.T) {
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
	request := func(method, path, session string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
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
	base := "/admin/bookmarks/apitoken/"
	if got := request(http.MethodGet, base+"add/", adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `name="name"`) || !strings.Contains(got.Body.String(), `name="user"`) || strings.Contains(got.Body.String(), `name="key"`) {
		t.Fatalf("add form: %d %s", got.Code, got.Body.String())
	}
	form := url.Values{"name": {"Alice API"}, "user": {strconv.FormatInt(alice.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", adminSession, form, false); got.Code != 403 {
		t.Fatalf("add without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != 302 || got.Header().Get("Location") != base {
		t.Fatalf("add token: %d %q %s", got.Code, got.Header().Get("Location"), got.Body.String())
	}
	var id int64
	var key string
	if err := db.QueryRowContext(ctx, `SELECT id,key FROM bookmarks_apitoken WHERE user_id=?`, alice.ID).Scan(&id, &key); err != nil {
		t.Fatal(err)
	}
	if decoded, err := hex.DecodeString(key); err != nil || len(decoded) != 20 {
		t.Fatalf("generated key: %q %v", key, err)
	}
	change := base + strconv.FormatInt(id, 10) + "/change/"
	if got := request(http.MethodGet, change, adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), "Alice API") || strings.Contains(got.Body.String(), key) {
		t.Fatalf("change form exposes key or omits name: %d %s", got.Code, got.Body.String())
	}
	bobForm := url.Values{"name": {"Service token"}, "user": {strconv.FormatInt(bob.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", adminSession, bobForm, true); got.Code != 302 {
		t.Fatalf("add second token: %d", got.Code)
	}
	filtered := request(http.MethodGet, base+"?q=alice&user__username=alice", adminSession, nil, false)
	if filtered.Code != 200 || !strings.Contains(filtered.Body.String(), "Alice API") || strings.Contains(filtered.Body.String(), "Service token") || !strings.Contains(filtered.Body.String(), `href="`+change+`"`) {
		t.Fatalf("filtered list: %d %s", filtered.Code, filtered.Body.String())
	}
	byUsername := request(http.MethodGet, base+"?q=bob", adminSession, nil, false)
	if byUsername.Code != 200 || !strings.Contains(byUsername.Body.String(), "Service token") || strings.Contains(byUsername.Body.String(), "Alice API") {
		t.Fatalf("search by username: %d %s", byUsername.Code, byUsername.Body.String())
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "apitoken", "view_apitoken")
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, change, viewerSession, nil, false); got.Code != 200 || strings.Contains(got.Body.String(), `value="Save"`) {
		t.Fatalf("view-only change form: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, change, viewerSession, form, true); got.Code != 403 {
		t.Fatalf("view-only change: %d", got.Code)
	}
	adder, err := users.CreateUser(ctx, auth.NewUser{Username: "adder", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, adder.ID, "bookmarks", "apitoken", "add_apitoken")
	adderSession, err := users.CreateSession(ctx, adder.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/admin/", adderSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `href="`+base+`add/"`) {
		t.Fatalf("add-only dashboard: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, base, adderSession, nil, false); got.Code != 403 {
		t.Fatalf("add-only list: %d", got.Code)
	}
	if got := request(http.MethodGet, base+"add/", adderSession, nil, false); got.Code != 200 {
		t.Fatalf("add-only form: %d", got.Code)
	}
	form.Set("name", "Renamed API")
	form.Set("user", strconv.FormatInt(bob.ID, 10))
	if got := request(http.MethodPost, change, adminSession, form, true); got.Code != 302 {
		t.Fatalf("change token: %d %s", got.Code, got.Body.String())
	}
	var updatedKey, updatedName string
	var updatedUser int64
	if err := db.QueryRowContext(ctx, `SELECT key,name,user_id FROM bookmarks_apitoken WHERE id=?`, id).Scan(&updatedKey, &updatedName, &updatedUser); err != nil || updatedKey != key || updatedName != "Renamed API" || updatedUser != bob.ID {
		t.Fatalf("updated token: %q %q %d %v", updatedKey, updatedName, updatedUser, err)
	}
	if got := request(http.MethodPost, base+strconv.FormatInt(id, 10)+"/delete/", adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 302 {
		t.Fatalf("delete token: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_apitoken WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted token remains: %d %v", count, err)
	}
}
