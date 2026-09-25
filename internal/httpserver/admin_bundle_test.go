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

func TestAdminBundleCreateChangeDeleteAndValidation(t *testing.T) {
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
	other, err := users.CreateUser(ctx, auth.NewUser{Username: "other", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	adminSession, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ownerSession, err := users.CreateSession(ctx, owner.ID, time.Hour)
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
	base := "/admin/bookmarks/bookmarkbundle/"
	form := url.Values{"name": {"Work"}, "search": {"golang"}, "any_tags": {"code"}, "all_tags": {"work"}, "excluded_tags": {"old"}, "filter_unread": {"yes"}, "filter_shared": {"no"}, "order": {"17"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodGet, base+"add/", ownerSession, nil, false); got.Code != 403 {
		t.Fatalf("non-staff add: %d", got.Code)
	}
	if got := request(http.MethodGet, base+"add/", adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `name="order" value="0"`) || strings.Contains(got.Body.String(), `name="date_created"`) || strings.Contains(got.Body.String(), `name="date_modified"`) {
		t.Fatalf("add form: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, false); got.Code != 403 {
		t.Fatalf("create without CSRF: %d", got.Code)
	}
	for _, test := range []struct{ name, field, value string }{
		{"empty name", "name", " "},
		{"long name", "name", strings.Repeat("x", 257)},
		{"long search", "search", strings.Repeat("x", 257)},
		{"long tags", "any_tags", strings.Repeat("x", 1025)},
		{"bad unread", "filter_unread", "maybe"},
		{"bad shared", "filter_shared", "maybe"},
		{"missing order", "order", ""},
		{"bad order", "order", "1.5"},
		{"bad owner", "owner", "99999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := url.Values{}
			for key, values := range form {
				invalid[key] = append([]string(nil), values...)
			}
			invalid.Set(test.field, test.value)
			got := request(http.MethodPost, base+"add/", adminSession, invalid, true)
			if got.Code != 200 || !strings.Contains(got.Body.String(), "errornote") {
				t.Fatalf("validation: %d %q", got.Code, got.Body.String())
			}
		})
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmarkbundle`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid create wrote records: %d %v", count, err)
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != 302 || got.Header().Get("Location") != base {
		t.Fatalf("create: %d %q %q", got.Code, got.Header().Get("Location"), got.Body.String())
	}
	var id, ownerID, order int64
	var name, search, anyTags, allTags, excludedTags, unread, shared string
	var created, modified time.Time
	read := func() {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT id,name,search,any_tags,all_tags,excluded_tags,filter_unread,filter_shared,"order",owner_id,date_created,date_modified FROM bookmarks_bookmarkbundle WHERE name=?`, name).Scan(&id, &name, &search, &anyTags, &allTags, &excludedTags, &unread, &shared, &order, &ownerID, &created, &modified); err != nil {
			t.Fatal(err)
		}
	}
	name = "Work"
	read()
	if search != "golang" || anyTags != "code" || allTags != "work" || excludedTags != "old" || unread != "yes" || shared != "no" || order != 17 || ownerID != owner.ID || created.IsZero() || modified.IsZero() {
		t.Fatalf("created fields: name=%q search=%q tags=%q/%q/%q filters=%q/%q order=%d owner=%d dates=%v/%v", name, search, anyTags, allTags, excludedTags, unread, shared, order, ownerID, created, modified)
	}
	change := base + strconv.FormatInt(id, 10) + "/change/"
	if got := request(http.MethodGet, base, adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `href="`+base+`add/"`) || !strings.Contains(got.Body.String(), `href="`+change+`"`) {
		t.Fatalf("list links: %d %q", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, change, adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `name="search" value="golang"`) {
		t.Fatalf("change form: %d %q", got.Code, got.Body.String())
	}
	for _, role := range []struct {
		name       string
		permission string
		check      func(string)
	}{
		{"viewer", "view_bookmarkbundle", func(session string) {
			if got := request(http.MethodGet, change, session, nil, false); got.Code != 200 || strings.Contains(got.Body.String(), `value="Save"`) || strings.Contains(got.Body.String(), `class="deletelink"`) {
				t.Fatalf("view-only form: %d %q", got.Code, got.Body.String())
			}
			if got := request(http.MethodPost, change, session, form, true); got.Code != 403 {
				t.Fatalf("view-only change: %d", got.Code)
			}
		}},
		{"adder", "add_bookmarkbundle", func(session string) {
			if got := request(http.MethodGet, base, session, nil, false); got.Code != 403 {
				t.Fatalf("add-only list: %d", got.Code)
			}
			if got := request(http.MethodGet, base+"add/", session, nil, false); got.Code != 200 {
				t.Fatalf("add-only form: %d", got.Code)
			}
			if got := request(http.MethodGet, change, session, nil, false); got.Code != 403 {
				t.Fatalf("add-only change: %d", got.Code)
			}
		}},
		{"deleter", "delete_bookmarkbundle", func(session string) {
			if got := request(http.MethodGet, change, session, nil, false); got.Code != 403 {
				t.Fatalf("delete-only change: %d", got.Code)
			}
			if got := request(http.MethodGet, base+strconv.FormatInt(id, 10)+"/delete/", session, nil, false); got.Code != 200 {
				t.Fatalf("delete-only confirmation: %d", got.Code)
			}
		}},
	} {
		staff, err := users.CreateUser(ctx, auth.NewUser{Username: role.name, Password: "password", IsStaff: true})
		if err != nil {
			t.Fatal(err)
		}
		grantTestUserPermission(t, db, staff.ID, "bookmarks", "bookmarkbundle", role.permission)
		session, err := users.CreateSession(ctx, staff.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		role.check(session)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmarkbundle SET date_created=?,date_modified=? WHERE id=?`, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), id); err != nil {
		t.Fatal(err)
	}
	form.Set("name", "Updated")
	form.Set("search", "changed")
	form.Set("filter_unread", "off")
	form.Set("filter_shared", "yes")
	form.Set("order", "-3")
	form.Set("owner", strconv.FormatInt(other.ID, 10))
	form.Set("all_tags", strings.Repeat("x", 1025))
	if got := request(http.MethodPost, change, adminSession, form, true); got.Code != 200 || !strings.Contains(got.Body.String(), "errornote") {
		t.Fatalf("invalid change: %d %q", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT name FROM bookmarks_bookmarkbundle WHERE id=?`, id).Scan(&name); err != nil || name != "Work" {
		t.Fatalf("invalid change modified record: %q %v", name, err)
	}
	form.Set("all_tags", "work")
	if got := request(http.MethodPost, change, adminSession, form, false); got.Code != 403 {
		t.Fatalf("change without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, change, adminSession, form, true); got.Code != 302 {
		t.Fatalf("change: %d %q", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT name,search,filter_unread,filter_shared,"order",owner_id,date_created,date_modified FROM bookmarks_bookmarkbundle WHERE id=?`, id).Scan(&name, &search, &unread, &shared, &order, &ownerID, &created, &modified); err != nil {
		t.Fatal(err)
	}
	if name != "Updated" || search != "changed" || unread != "off" || shared != "yes" || order != -3 || ownerID != other.ID || created.Year() != 2020 || !modified.After(created) {
		t.Fatalf("updated fields: %q %q %q %q %d %d %v %v", name, search, unread, shared, order, ownerID, created, modified)
	}
	deletePath := base + strconv.FormatInt(id, 10) + "/delete/"
	if got := request(http.MethodGet, deletePath, adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), "Updated") {
		t.Fatalf("delete confirmation: %d", got.Code)
	}
	if got := request(http.MethodPost, deletePath, adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, false); got.Code != 403 {
		t.Fatalf("delete without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, deletePath, adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 302 {
		t.Fatalf("delete: %d", got.Code)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmarkbundle WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bundle remains: %d %v", count, err)
	}
}
