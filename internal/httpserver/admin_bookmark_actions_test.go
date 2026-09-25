package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminBookmarkActionsRespectChangelistAndFiles(t *testing.T) {
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
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "bookmark", "view_bookmark")
	adminSession, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
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
	ids := map[string]int64{}
	for _, item := range []struct {
		key, title, preview string
		owner               int64
	}{
		{"alice-match", "match Alpha", "preview.png", alice.ID},
		{"alice-other", "Other", "", alice.ID},
		{"bob-match", "match Beta", "", bob.ID},
	} {
		result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES (?,?,?,?,?,NULL,NULL,0,0,0,?,?,?,'','',?)`, "https://example.test/"+item.key, "", item.title, "", "", added, added, item.owner, item.preview)
		if err != nil {
			t.Fatal(err)
		}
		ids[item.key], err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES (?,'snapshot.html',4,'snapshot','text/html','Snapshot','complete',0,?)`, added, ids["alice-match"]); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ directory, name string }{{"assets", "snapshot.html"}, {"previews", "preview.png"}} {
		directory := filepath.Join(cfg.DataDir, item.directory)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, item.name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
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
	path := "/admin/bookmarks/bookmark/?q=match&owner__username=alice"
	list := request(http.MethodGet, path, adminSession, nil, true)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `value="archive_selected_bookmarks"`) || !strings.Contains(list.Body.String(), `name="_selected_action"`) {
		t.Fatalf("bookmark action list: %d %s", list.Code, list.Body.String())
	}
	viewerList := request(http.MethodGet, path, viewerSession, nil, true)
	if viewerList.Code != http.StatusOK || strings.Contains(viewerList.Body.String(), `value="delete_selected_bookmarks"`) {
		t.Fatalf("view-only action list: %d %s", viewerList.Code, viewerList.Body.String())
	}
	form := url.Values{"action": {"archive_selected_bookmarks"}, "select_across": {"1"}, "_selected_action": {strconv.FormatInt(ids["bob-match"], 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, path, adminSession, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("action without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, path, aliceSession, form, true); got.Code != http.StatusForbidden {
		t.Fatalf("nonstaff action: %d", got.Code)
	}
	if got := request(http.MethodPost, path, viewerSession, form, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only action: %d", got.Code)
	}
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != path {
		t.Fatalf("archive across filtered list: %d %s", got.Code, got.Body.String())
	}
	var archived, unread bool
	var modified time.Time
	var normalized string
	if err := db.QueryRowContext(ctx, `SELECT is_archived,unread,date_modified,url_normalized FROM bookmarks_bookmark WHERE id=?`, ids["alice-match"]).Scan(&archived, &unread, &modified, &normalized); err != nil || !archived || unread || !modified.After(added) || normalized != "https://example.test/alice-match" {
		t.Fatalf("archived bookmark: archived=%t unread=%t modified=%v normalized=%q err=%v", archived, unread, modified, normalized, err)
	}
	archivedAt := modified
	for _, key := range []string{"alice-other", "bob-match"} {
		if err := db.QueryRowContext(ctx, `SELECT is_archived FROM bookmarks_bookmark WHERE id=?`, ids[key]).Scan(&archived); err != nil || archived {
			t.Fatalf("unfiltered bookmark %s changed: %t %v", key, archived, err)
		}
	}
	form.Set("select_across", "0")
	form.Set("_selected_action", strconv.FormatInt(ids["alice-match"], 10))
	form.Set("action", "mark_as_unread")
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("mark unread: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT unread,date_modified FROM bookmarks_bookmark WHERE id=?`, ids["alice-match"]).Scan(&unread, &modified); err != nil || !unread || !modified.Equal(archivedAt) {
		t.Fatalf("mark unread modified timestamp: unread=%t modified=%v err=%v", unread, modified, err)
	}
	form.Set("action", "mark_as_read")
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("mark read: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT unread FROM bookmarks_bookmark WHERE id=?`, ids["alice-match"]).Scan(&unread); err != nil || unread {
		t.Fatalf("mark read: unread=%t err=%v", unread, err)
	}
	form.Set("action", "unarchive_selected_bookmarks")
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("unarchive: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT is_archived FROM bookmarks_bookmark WHERE id=?`, ids["alice-match"]).Scan(&archived); err != nil || archived {
		t.Fatalf("unarchive: archived=%t err=%v", archived, err)
	}
	form.Set("action", "delete_selected_bookmarks")
	form.Add("_selected_action", strconv.FormatInt(ids["bob-match"], 10))
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound || !strings.Contains(got.Header().Get("Set-Cookie"), "ld_admin_bookmark_action=") {
		t.Fatalf("delete selected: %d %s", got.Code, got.Body.String())
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=?`, ids["alice-match"]).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("selected bookmark remains: %d %v", remaining, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmarkasset WHERE bookmark_id=?`, ids["alice-match"]).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("selected asset remains: %d %v", remaining, err)
	}
	for _, item := range []struct{ directory, name string }{{"assets", "snapshot.html"}, {"previews", "preview.png"}} {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, item.directory, item.name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted bookmark file remains: %s/%s: %v", item.directory, item.name, err)
		}
	}
	for _, key := range []string{"alice-other", "bob-match"} {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=?`, ids[key]).Scan(&remaining); err != nil || remaining != 1 {
			t.Fatalf("unfiltered bookmark %s deleted: %d %v", key, remaining, err)
		}
	}
}

func TestAdminBookmarkActionsPostgres(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bookmark_action_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM bookmarks_bookmark WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, admin.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	ids := map[string]int64{}
	added := time.Now().UTC().Add(-time.Hour)
	for _, name := range []string{"focus", "other"} {
		var id int64
		if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ($1,'',$2,'','',NULL,NULL,false,false,false,$3,$4,$5,'','','') RETURNING id`, "https://example.test/"+name, name, added, added, admin.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/bookmarks/bookmark/?q=focus&owner__username=" + url.QueryEscape(admin.Username)
	form := url.Values{"action": {"mark_as_unread"}, "select_across": {"0"}, "csrfmiddlewaretoken": {csrf}, "_selected_action": {strconv.FormatInt(ids["focus"], 10), strconv.FormatInt(ids["other"], 10)}}
	handler := New(db, config.Config{DBEngine: "postgres", DataDir: t.TempDir()}, t.TempDir())
	listRequest := httptest.NewRequest(http.MethodGet, path, nil)
	listRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "focus") || strings.Contains(listResponse.Body.String(), `>other</a>`) {
		t.Fatalf("PostgreSQL filtered list: %d %s", listResponse.Code, listResponse.Body.String())
	}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL mark unread: %d %s", got.Code, got.Body.String())
	}
	var focusUnread, otherUnread bool
	if err := db.QueryRowContext(ctx, `SELECT unread FROM bookmarks_bookmark WHERE id=$1`, ids["focus"]).Scan(&focusUnread); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT unread FROM bookmarks_bookmark WHERE id=$1`, ids["other"]).Scan(&otherUnread); err != nil || !focusUnread || otherUnread {
		t.Fatalf("PostgreSQL filtered update: focus=%t other=%t err=%v", focusUnread, otherUnread, err)
	}
	form.Set("action", "archive_selected_bookmarks")
	form.Set("select_across", "1")
	if got := request(); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL archive: %d %s", got.Code, got.Body.String())
	}
	var archived bool
	if err := db.QueryRowContext(ctx, `SELECT is_archived FROM bookmarks_bookmark WHERE id=$1`, ids["focus"]).Scan(&archived); err != nil || !archived {
		t.Fatalf("PostgreSQL archive state: %t %v", archived, err)
	}
	form.Set("action", "delete_selected_bookmarks")
	form.Set("select_across", "0")
	if got := request(); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL delete: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=$1`, ids["focus"]).Scan(&count); err != nil || count != 0 {
		t.Fatalf("PostgreSQL selected bookmark remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=$1`, ids["other"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("PostgreSQL unfiltered bookmark changed: %d %v", count, err)
	}
}
