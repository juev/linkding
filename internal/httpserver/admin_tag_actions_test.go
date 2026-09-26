package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminDeleteUnusedTagsRespectsFilteredSelectionAndViewPermission(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, cfg.DBEngine)
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "tag", "view_tag")
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	aliceSession, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	added := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	tagIDs := map[string]int64{}
	for _, item := range []struct {
		name  string
		owner int64
	}{
		{"match-unused", alice.ID},
		{"match-used", alice.ID},
		{"plain-unused", alice.ID},
		{"match-bob", bob.ID},
	} {
		result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (?,?,?)`, item.name, added, item.owner)
		if err != nil {
			t.Fatal(err)
		}
		tagIDs[item.name], err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test','https://example.test','Example','','',NULL,NULL,0,0,0,?,?,?,'','','')`, added, added, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookmarkID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) VALUES (?,?)`, bookmarkID, tagIDs["match-used"]); err != nil {
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
	path := "/admin/bookmarks/tag/?q=match&owner__username=alice"
	list := request(http.MethodGet, path, viewerSession, nil, true)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `value="delete_unused_tags"`) || !strings.Contains(list.Body.String(), `name="_selected_action"`) {
		t.Fatalf("view-only action list: %d %s", list.Code, list.Body.String())
	}
	form := url.Values{"action": {"delete_unused_tags"}, "select_across": {"0"}, "csrfmiddlewaretoken": {csrf}}
	for _, name := range []string{"match-unused", "match-used", "match-bob"} {
		form.Add("_selected_action", strconv.FormatInt(tagIDs[name], 10))
	}
	if got := request(http.MethodPost, path, viewerSession, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("action without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, path, aliceSession, form, true); got.Code != http.StatusForbidden {
		t.Fatalf("non-staff action: %d", got.Code)
	}
	response := request(http.MethodPost, path, viewerSession, form, true)
	if response.Code != http.StatusFound || response.Header().Get("Location") != path {
		t.Fatalf("filtered action: %d %q %s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	for name, want := range map[string]int{"match-unused": 0, "match-used": 1, "plain-unused": 1, "match-bob": 1} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=?`, tagIDs[name]).Scan(&count); err != nil || count != want {
			t.Fatalf("tag %s after filtered action: %d, want %d: %v", name, count, want, err)
		}
	}
	if !strings.Contains(response.Header().Get("Set-Cookie"), "ld_admin_tag_action=") {
		t.Fatal("action success message was not set")
	}
	form.Set("select_across", "1")
	if got := request(http.MethodPost, "/admin/bookmarks/tag/?q=match&owner__username=bob", viewerSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("select across action: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=?`, tagIDs["match-bob"]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("select across did not delete filtered Bob tag: %d %v", count, err)
	}
	form.Set("action", "delete_selected")
	if got := request(http.MethodPost, path, viewerSession, form, true); got.Code != http.StatusBadRequest {
		t.Fatalf("unsupported action: %d", got.Code)
	}
	if strings.Contains(list.Body.String(), `value="delete_selected"`) {
		t.Fatal("view-only staff saw the delete action")
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "tag", "delete_tag")
	list = request(http.MethodGet, path, viewerSession, nil, true)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `value="delete_selected"`) {
		t.Fatalf("delete-permitted action list: %d", list.Code)
	}
	form.Set("select_across", "0")
	form.Del("_selected_action")
	form.Add("_selected_action", strconv.FormatInt(tagIDs["match-used"], 10))
	form.Add("_selected_action", strconv.FormatInt(tagIDs["plain-unused"], 10))
	confirmation := request(http.MethodPost, path, viewerSession, form, true)
	if confirmation.Code != http.StatusOK || !strings.Contains(confirmation.Body.String(), `name="post" value="yes"`) || !strings.Contains(confirmation.Body.String(), `href="/admin/bookmarks/tag/`+strconv.FormatInt(tagIDs["match-used"], 10)+`/change/">match-used</a>`) || strings.Contains(confirmation.Body.String(), `plain-unused`) {
		t.Fatalf("filtered delete confirmation: %d %s", confirmation.Code, confirmation.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE tag_id=?`, tagIDs["match-used"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("confirmation changed tag relations: %d %v", count, err)
	}
	form.Set("post", "yes")
	confirmed := request(http.MethodPost, path, viewerSession, form, true)
	if confirmed.Code != http.StatusFound || confirmed.Header().Get("Location") != path {
		t.Fatalf("confirmed delete: %d %s", confirmed.Code, confirmed.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=?`, tagIDs["match-used"]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("selected used tag remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE tag_id=?`, tagIDs["match-used"]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("selected tag relation remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=?`, tagIDs["plain-unused"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("tag outside filter changed: %d %v", count, err)
	}
}

func TestAdminTagActionsPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "postgres")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("tag_action_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM django_admin_log WHERE user_id = $1`,
			`DELETE FROM bookmarks_tag WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	var tagID int64
	err = db.QueryRowContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES ($1,$2,$3) RETURNING id`, "literal%unused", time.Now().UTC(), user.ID).Scan(&tagID)
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
	path := "/admin/bookmarks/tag/?q=%25&owner__username=" + url.QueryEscape(user.Username)
	form := url.Values{"action": {"delete_unused_tags"}, "select_across": {"0"}, "_selected_action": {strconv.FormatInt(tagID, 10)}, "csrfmiddlewaretoken": {csrf}}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	New(db, config.Config{DBEngine: "postgres"}, t.TempDir()).ServeHTTP(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("PostgreSQL tag action: %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=$1`, tagID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("PostgreSQL tag not deleted: %d %v", count, err)
	}
	var selectedID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES ($1,$2,$3) RETURNING id`, "literal%selected", time.Now().UTC(), user.ID).Scan(&selectedID); err != nil {
		t.Fatal(err)
	}
	form.Set("action", "delete_selected")
	form.Set("_selected_action", strconv.FormatInt(selectedID, 10))
	for _, confirmed := range []bool{false, true} {
		if confirmed {
			form.Set("post", "yes")
		}
		request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		request.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		response = httptest.NewRecorder()
		New(db, config.Config{DBEngine: "postgres"}, t.TempDir()).ServeHTTP(response, request)
		want := http.StatusOK
		if confirmed {
			want = http.StatusFound
		}
		if response.Code != want {
			t.Fatalf("PostgreSQL confirmed=%v: %d %s", confirmed, response.Code, response.Body.String())
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_tag WHERE id=$1`, selectedID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("PostgreSQL selected tag not deleted: %d %v", count, err)
	}
}
