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

func TestAdminTagCRUDAndPermissions(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), TimeZone: "Europe/Moscow"}
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
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "owner", Password: "password"})
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
	base := "/admin/bookmarks/tag/"
	if got := request(http.MethodGet, base+"add/", ownerSession, nil, false); got.Code != 403 {
		t.Fatalf("non-staff add: %d", got.Code)
	}
	if got := request(http.MethodGet, base+"add/", adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `name="date_added_0"`) || !strings.Contains(got.Body.String(), `name="date_added_1"`) {
		t.Fatalf("add form: %d %s", got.Code, got.Body.String())
	}
	form := url.Values{"name": {"  tag with spaces  "}, "date_added_0": {"2020-02-03"}, "date_added_1": {"04:05:06"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", adminSession, form, false); got.Code != 403 {
		t.Fatalf("add without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, base+"add/", adminSession, url.Values{"name": {""}, "date_added_0": {"2020-02-03"}, "date_added_1": {"04:05:06"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 200 || !strings.Contains(got.Body.String(), "errornote") {
		t.Fatalf("invalid tag accepted: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, url.Values{"name": {" \t "}, "date_added_0": {"2020-02-03"}, "date_added_1": {"04:05:06"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 200 || !strings.Contains(got.Body.String(), "errornote") {
		t.Fatalf("whitespace-only tag accepted: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, url.Values{"name": {"tag"}, "date_added_0": {"2020-02-03"}, "date_added_1": {"04:05"}, "owner": {"999999"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 200 || !strings.Contains(got.Body.String(), "Select a valid owner") {
		t.Fatalf("invalid owner accepted: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != 302 || got.Header().Get("Location") != base {
		t.Fatalf("create tag: %d %q %s", got.Code, got.Header().Get("Location"), got.Body.String())
	}
	var id int64
	var added time.Time
	if err := db.QueryRowContext(ctx, `SELECT id,date_added FROM bookmarks_tag WHERE name=? AND owner_id=?`, "tag with spaces", owner.ID).Scan(&id, &added); err != nil {
		t.Fatal(err)
	}
	if !added.Equal(time.Date(2020, 2, 3, 1, 5, 6, 0, time.UTC)) {
		t.Fatalf("stored date_added: %s", added)
	}
	change := base + strconv.FormatInt(id, 10) + "/change/"
	if got := request(http.MethodGet, change, adminSession, nil, false); got.Code != 200 || !strings.Contains(got.Body.String(), `value="2020-02-03"`) || !strings.Contains(got.Body.String(), `value="04:05:06"`) {
		t.Fatalf("change form date display: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, base+"999999/change/", adminSession, nil, false); got.Code != 404 {
		t.Fatalf("missing change object: %d", got.Code)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO django_content_type(id,app_label,model) VALUES (301,'bookmarks','tag')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_permission(id,name,content_type_id,codename) VALUES (301,'Can view tag',301,'view_tag')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_user_user_permissions(user_id,permission_id) VALUES (?,301)`, viewer.ID); err != nil {
		t.Fatal(err)
	}
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, change, viewerSession, nil, false); got.Code != 200 || strings.Contains(got.Body.String(), `value="Save"`) {
		t.Fatalf("view-only form: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, change, viewerSession, form, true); got.Code != 403 {
		t.Fatalf("view-only change: %d", got.Code)
	}
	missingDate := url.Values{"name": {"unchanged"}, "owner": {strconv.FormatInt(owner.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, change, adminSession, missingDate, true); got.Code != 200 || !strings.Contains(got.Body.String(), "valid date and time") {
		t.Fatalf("change accepted missing date/time: %d %s", got.Code, got.Body.String())
	}
	form.Set("name", "  updated tag  ")
	if got := request(http.MethodPost, change, adminSession, form, true); got.Code != 302 {
		t.Fatalf("change tag preserving date: %d %s", got.Code, got.Body.String())
	}
	var name string
	var changedDate time.Time
	if err := db.QueryRowContext(ctx, `SELECT name,date_added FROM bookmarks_tag WHERE id=?`, id).Scan(&name, &changedDate); err != nil || name != "updated tag" || !changedDate.Equal(added) {
		t.Fatalf("tag after change: %q %s (initial %s): %v", name, changedDate, added, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark (url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test','https://example.test','Example','','',NULL,NULL,0,0,0,?,?,?,'','','')`, added, added, owner.ID); err != nil {
		t.Fatal(err)
	}
	var bookmarkID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmark WHERE url='https://example.test'`).Scan(&bookmarkID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) VALUES (?,?)`, bookmarkID, id); err != nil {
		t.Fatal(err)
	}
	deletePath := base + strconv.FormatInt(id, 10) + "/delete/"
	if got := request(http.MethodGet, base+"999999/delete/", adminSession, nil, false); got.Code != 404 {
		t.Fatalf("missing delete object: %d", got.Code)
	}
	if got := request(http.MethodGet, deletePath, ownerSession, nil, false); got.Code != 403 {
		t.Fatalf("non-staff delete: %d", got.Code)
	}
	if got := request(http.MethodPost, deletePath, adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != 302 {
		t.Fatalf("delete tag: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tag remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE tag_id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tag associations remain: %d %v", count, err)
	}
}

func TestAdminTagTimeZoneValidation(t *testing.T) {
	location, err := adminTagLocation("")
	if err != nil || location.String() != "UTC" {
		t.Fatalf("empty timezone should default to UTC, got %v, %v", location, err)
	}
	if _, err := adminTagLocation("unknown/timezone"); err == nil {
		t.Fatal("unknown timezone was accepted")
	}
}
