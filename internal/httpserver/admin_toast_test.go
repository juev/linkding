package httpserver

import (
	"context"
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

func TestAdminToastCreateChangeDeletePermissionsAndCSRF(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	adminKey, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ownerKey, err := users.CreateSession(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path, session string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		body := strings.NewReader("")
		if form != nil {
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
	base := "/admin/bookmarks/toast/"
	if got := request(http.MethodGet, base+"add/", ownerKey, nil, false); got.Code != http.StatusFound || got.Header().Get("Location") != "/admin/login/?next="+base+"add/" {
		t.Fatalf("non-staff add: %d", got.Code)
	}
	if got := request(http.MethodGet, base+"add/", adminKey, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), "name=\"message\"") || !strings.Contains(got.Body.String(), `href="/admin/auth/user/add/?_to_field=id&amp;_popup=1"`) || !strings.Contains(got.Body.String(), `value="Save and add another" name="_addanother"`) || !strings.Contains(got.Body.String(), `value="Save and continue editing" name="_continue"`) {
		t.Fatalf("add form: %d", got.Code)
	}
	form := url.Values{"key": {"notice"}, "message": {"Important"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", adminKey, form, false); got.Code != 403 {
		t.Fatalf("missing CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, base+"add/", adminKey, form, true); got.Code != 302 || got.Header().Get("Location") != base {
		t.Fatalf("create toast: %d %q", got.Code, got.Header().Get("Location"))
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key=? AND owner_id=?`, "notice", owner.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	addAnother := url.Values{"key": {"notice-again"}, "message": {"Another message"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}, "_addanother": {"Save and add another"}}
	if got := request(http.MethodPost, base+"add/", adminKey, addAnother, true); got.Code != 302 || got.Header().Get("Location") != base+"add/" {
		t.Fatalf("save and add another toast: %d %q", got.Code, got.Header().Get("Location"))
	}
	change := base + strconv.FormatInt(id, 10) + "/change/"
	list := request(http.MethodGet, base, adminKey, nil, false)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `href="`+base+`add/"`) || !strings.Contains(list.Body.String(), `href="`+change+`"`) {
		t.Fatalf("toast list links: %d %q", list.Code, list.Body.String())
	}
	if got := request(http.MethodGet, change, adminKey, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), "Important") {
		t.Fatalf("change form: %d", got.Code)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "toast", "view_toast")
	adder, err := users.CreateUser(ctx, auth.NewUser{Username: "adder", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, adder.ID, "bookmarks", "toast", "add_toast")
	adderKey, err := users.CreateSession(ctx, adder.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, "/admin/", adderKey, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `href="`+base+`add/"`) {
		t.Fatalf("add-only dashboard: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, base, adderKey, nil, false); got.Code != 403 {
		t.Fatalf("add-only list: %d", got.Code)
	}
	if got := request(http.MethodGet, base+"add/", adderKey, nil, false); got.Code != 200 {
		t.Fatalf("add-only form: %d", got.Code)
	}
	viewerKey, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, change, viewerKey, nil, false); got.Code != 200 || strings.Contains(got.Body.String(), `value="Save"`) || strings.Contains(got.Body.String(), `class="deletelink"`) {
		t.Fatalf("view-only form: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, change, viewerKey, form, true); got.Code != 403 {
		t.Fatalf("view-only change: %d", got.Code)
	}
	form.Set("message", "Updated")
	form.Set("acknowledged", "on")
	if got := request(http.MethodPost, change, adminKey, form, true); got.Code != 302 || got.Header().Get("Location") != base {
		t.Fatalf("change toast: %d", got.Code)
	}
	form.Set("_continue", "Save and continue editing")
	if got := request(http.MethodPost, change, adminKey, form, true); got.Code != 302 || got.Header().Get("Location") != change {
		t.Fatalf("save and continue editing toast: %d %q", got.Code, got.Header().Get("Location"))
	}
	var message string
	var acknowledged bool
	if err := db.QueryRowContext(ctx, `SELECT message,acknowledged FROM bookmarks_toast WHERE id=?`, id).Scan(&message, &acknowledged); err != nil || message != "Updated" || !acknowledged {
		t.Fatalf("updated toast: %q %t %v", message, acknowledged, err)
	}
	deletePath := base + strconv.FormatInt(id, 10) + "/delete/"
	if got := request(http.MethodPost, deletePath, adminKey, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 302 {
		t.Fatalf("delete toast: %d", got.Code)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_toast WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("toast remains after delete: %d %v", count, err)
	}
}
