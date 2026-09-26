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

func adminBookmarkTestForm(ownerID, tagID int64, csrf string) url.Values {
	return url.Values{
		"url":                 {"https://example.test/admin"},
		"url_normalized":      {"deliberately-wrong"},
		"title":               {"Admin title"},
		"description":         {"A description"},
		"notes":               {"A note"},
		"website_title":       {"Website title"},
		"unread":              {"on"},
		"shared":              {"on"},
		"date_added_0":        {"2020-01-02"},
		"date_added_1":        {"03:04:05"},
		"date_modified_0":     {"2020-01-03"},
		"date_modified_1":     {"04:05:06"},
		"date_accessed_0":     {"2020-01-04"},
		"date_accessed_1":     {"05:06:07"},
		"owner":               {strconv.FormatInt(ownerID, 10)},
		"tags":                {strconv.FormatInt(tagID, 10)},
		"csrfmiddlewaretoken": {csrf},
	}
}

func TestAdminBookmarkCreateChangeDelete(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), TimeZone: "Europe/Moscow"}
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
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "owner", Password: "password"})
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
	ownerSession, err := users.CreateSession(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	var tagOne, tagTwo int64
	for i, name := range []string{"one", "two"} {
		result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (?,?,?)`, name, time.Now().UTC(), owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			tagOne = id
		} else {
			tagTwo = id
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
	base := "/admin/bookmarks/bookmark/"
	form := adminBookmarkTestForm(owner.ID, tagOne, csrf)
	if got := request(http.MethodGet, base+"add/", ownerSession, nil, true); got.Code != http.StatusForbidden {
		t.Fatalf("nonstaff add: %d", got.Code)
	}
	addPage := request(http.MethodGet, base+"add/", adminSession, nil, true)
	if addPage.Code != http.StatusOK || !strings.Contains(addPage.Body.String(), `name="tags" multiple`) || !strings.Contains(addPage.Body.String(), `name="latest_snapshot"`) {
		t.Fatalf("add form: %d %s", addPage.Code, addPage.Body.String())
	}
	if !strings.Contains(addPage.Body.String(), `value="Save and add another"`) || !strings.Contains(addPage.Body.String(), `value="Save and continue editing"`) || !strings.Contains(addPage.Body.String(), `class="vDateField"`) {
		t.Fatalf("add form controls: %d %s", addPage.Code, addPage.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("add without CSRF: %d", got.Code)
	}
	invalid := url.Values{}
	for key, values := range form {
		invalid[key] = append([]string(nil), values...)
	}
	invalid.Del("tags")
	if got := request(http.MethodPost, base+"add/", adminSession, invalid, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Select at least one tag") {
		t.Fatalf("missing tag validation: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base {
		t.Fatalf("add: %d %s", got.Code, got.Body.String())
	}
	var bookmarkID int64
	var normalized, title, note string
	var unread, shared bool
	var added, modified time.Time
	var accessed sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT id,url_normalized,title,notes,unread,shared,date_added,date_modified,date_accessed FROM bookmarks_bookmark WHERE owner_id=?`, owner.ID).Scan(&bookmarkID, &normalized, &title, &note, &unread, &shared, &added, &modified, &accessed); err != nil {
		t.Fatal(err)
	}
	if normalized != "https://example.test/admin" || title != "Admin title" || note != "A note" || !unread || !shared || !accessed.Valid || added.UTC().Hour() != 0 || modified.UTC().Hour() != 1 {
		t.Fatalf("saved bookmark: normalized=%q title=%q note=%q unread=%t shared=%t added=%v modified=%v accessed=%v", normalized, title, note, unread, shared, added, modified, accessed)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=? AND tag_id=?`, bookmarkID, tagOne).Scan(&count); err != nil || count != 1 {
		t.Fatalf("initial tag: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES (?,'snapshot.html',4,'snapshot','text/html','Snapshot','complete',0,?)`, time.Now().UTC(), bookmarkID); err != nil {
		t.Fatal(err)
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkasset WHERE bookmark_id=?`, bookmarkID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file,latest_snapshot_id) VALUES ('https://example.test/other','https://example.test/other','Other','','',NULL,NULL,0,0,0,?,?,?,'','','',?)`, time.Now().UTC(), time.Now().UTC(), owner.ID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := result.LastInsertId()
	if err != nil {
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
	changePath := base + strconv.FormatInt(bookmarkID, 10) + "/change/"
	changePage := request(http.MethodGet, changePath, viewerSession, nil, true)
	if changePage.Code != http.StatusOK || !strings.Contains(changePage.Body.String(), `value="Admin title"`) || strings.Contains(changePage.Body.String(), `value="Save"`) {
		t.Fatalf("view-only form: %d %s", changePage.Code, changePage.Body.String())
	}
	if got := request(http.MethodPost, changePath, viewerSession, form, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only change: %d", got.Code)
	}
	form.Set("url", "https://example.test/changed")
	form.Set("url_normalized", "wrong-again")
	form.Set("tags", strconv.FormatInt(tagTwo, 10))
	form.Set("latest_snapshot", strconv.FormatInt(assetID, 10))
	form.Set("preview_image_file", "preview.png")
	form.Set("is_archived", "on")
	if got := request(http.MethodPost, changePath, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("change: %d %s", got.Code, got.Body.String())
	}
	var archived bool
	var snapshot sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT url_normalized,is_archived,latest_snapshot_id FROM bookmarks_bookmark WHERE id=?`, bookmarkID).Scan(&normalized, &archived, &snapshot); err != nil || normalized != "https://example.test/changed" || !archived || !snapshot.Valid || snapshot.Int64 != assetID {
		t.Fatalf("updated fields: normalized=%q archived=%t snapshot=%v err=%v", normalized, archived, snapshot, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=? AND tag_id=?`, bookmarkID, tagOne).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old tag remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=? AND tag_id=?`, bookmarkID, tagTwo).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new tag missing: %d %v", count, err)
	}
	historyPath := base + strconv.FormatInt(bookmarkID, 10) + "/history/"
	if got := request(http.MethodGet, historyPath, adminSession, nil, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Change history: Admin title") || !strings.Contains(got.Body.String(), "Added.") || !strings.Contains(got.Body.String(), "Changed Url") {
		t.Fatalf("bookmark history: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, historyPath, adminSession, form, true); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("history accepted POST: %d", got.Code)
	}
	deletePath := base + strconv.FormatInt(bookmarkID, 10) + "/delete/"
	if got := request(http.MethodGet, deletePath, viewerSession, nil, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only delete: %d", got.Code)
	}
	if got := request(http.MethodPost, deletePath, adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != http.StatusFound {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=?`, bookmarkID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bookmark remains: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id=?`, otherID).Scan(&snapshot); err != nil || snapshot.Valid {
		t.Fatalf("other bookmark retained deleted asset: %v %v", snapshot, err)
	}
	for _, item := range []struct{ directory, name string }{{"assets", "snapshot.html"}, {"previews", "preview.png"}} {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, item.directory, item.name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file remains: %s/%s: %v", item.directory, item.name, err)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT action_flag,object_repr,change_message FROM django_admin_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var logFlags []int
	var logReprs, logMessages []string
	for rows.Next() {
		var flag int
		var repr, message string
		if err := rows.Scan(&flag, &repr, &message); err != nil {
			t.Fatal(err)
		}
		logFlags = append(logFlags, flag)
		logReprs = append(logReprs, repr)
		logMessages = append(logMessages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(logFlags) != 3 || logFlags[0] != 1 || logFlags[1] != 2 || logFlags[2] != 3 || logMessages[0] != adminAdditionMessage || logMessages[1] != adminChangeMessage([]string{"Url", "Url normalized", "Preview image file", "Is archived", "Tags", "Latest snapshot"}) || logMessages[2] != "" || logReprs[0] != adminBookmarkRepr("Admin title", "https://example.test/admin") || logReprs[1] != adminBookmarkRepr("Admin title", "https://example.test/changed") || logReprs[2] != logReprs[1] {
		t.Fatalf("bookmark admin logs: flags=%v reprs=%v messages=%v", logFlags, logReprs, logMessages)
	}
	form.Set("url", "https://example.test/continue")
	form.Del("latest_snapshot")
	form.Set("_continue", "Save and continue editing")
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != http.StatusFound || !strings.HasSuffix(got.Header().Get("Location"), "/change/") {
		t.Fatalf("save and continue redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	form.Del("_continue")
	form.Set("_addanother", "Save and add another")
	form.Set("url", "https://example.test/add-another")
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base+"add/" {
		t.Fatalf("save and add another redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
}

func TestAdminBookmarkPostgres(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bookmark_form_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM django_admin_log WHERE user_id=$1`, `DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id=$1)`, `DELETE FROM bookmarks_bookmark WHERE owner_id=$1`, `DELETE FROM bookmarks_tag WHERE owner_id=$1`, `DELETE FROM bookmarks_userprofile WHERE user_id=$1`, `DELETE FROM auth_user WHERE id=$1`} {
			if _, err := db.ExecContext(context.Background(), query, admin.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	var tagID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES ('admin-form-test',$1,$2) RETURNING id`, time.Now().UTC(), admin.ID).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DBEngine: "postgres", DataDir: t.TempDir()}
	handler := New(db, cfg, t.TempDir())
	request := func(path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/admin/bookmarks/bookmark/"
	form := adminBookmarkTestForm(admin.ID, tagID, csrf)
	if got := request(base+"add/", form); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL create: %d %s", got.Code, got.Body.String())
	}
	var id int64
	var normalized string
	if err := db.QueryRowContext(ctx, `SELECT id,url_normalized FROM bookmarks_bookmark WHERE owner_id=$1`, admin.ID).Scan(&id, &normalized); err != nil || normalized != "https://example.test/admin" {
		t.Fatalf("PostgreSQL bookmark: id=%d normalized=%q err=%v", id, normalized, err)
	}
	form.Set("title", "PostgreSQL changed")
	if got := request(base+strconv.FormatInt(id, 10)+"/change/", form); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL change: %d %s", got.Code, got.Body.String())
	}
	var assetID, otherID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES ($1,'',0,'snapshot','text/html','Snapshot','complete',false,$2) RETURNING id`, time.Now().UTC(), id).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file,latest_snapshot_id) VALUES ('https://example.test/other','https://example.test/other','Other','','',NULL,NULL,false,false,false,$1,$2,$3,'','','',$4) RETURNING id`, time.Now().UTC(), time.Now().UTC(), admin.ID, assetID).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if got := request(base+strconv.FormatInt(id, 10)+"/delete/", url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL delete: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("PostgreSQL bookmark remains: %d %v", count, err)
	}
	var snapshot sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id=$1`, otherID).Scan(&snapshot); err != nil || snapshot.Valid {
		t.Fatalf("PostgreSQL related snapshot remains: %v %v", snapshot, err)
	}
}
