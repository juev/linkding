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

func TestAdminBookmarkAssetCreateChangeDelete(t *testing.T) {
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
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "bookmarkasset", "view_bookmarkasset")
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
	added := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test/a','https://example.test/a','Example','','',NULL,NULL,0,0,0,?,?,?,'','','')`, added, added, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookmarkID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test/b','https://example.test/b','AAA','','',NULL,NULL,0,0,0,?,?,?,'','','')`, added.Add(time.Hour), added.Add(time.Hour), owner.ID); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(cfg.DataDir, "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	assetPath := filepath.Join(assetDir, "stored.html")
	if err := os.WriteFile(assetPath, []byte("test file"), 0o644); err != nil {
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
	base := "/admin/bookmarks/bookmarkasset/"
	form := url.Values{"bookmark": {strconv.FormatInt(bookmarkID, 10)}, "file": {"stored.html"}, "file_size": {"999"}, "asset_type": {"snapshot"}, "content_type": {"text/html"}, "display_name": {"Snapshot"}, "status": {"complete"}, "gzip": {"on"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodGet, base+"add/", ownerSession, nil, false); got.Code != http.StatusForbidden {
		t.Fatalf("nonstaff add: %d", got.Code)
	}
	addPage := request(http.MethodGet, base+"add/", adminSession, nil, false)
	if addPage.Code != http.StatusOK || !strings.Contains(addPage.Body.String(), `name="file"`) || !strings.Contains(addPage.Body.String(), `name="bookmark"`) || strings.Contains(addPage.Body.String(), `name="date_created"`) {
		t.Fatalf("asset add form: %d %s", addPage.Code, addPage.Body.String())
	}
	addHTML := addPage.Body.String()
	if first, second := strings.Index(addHTML, `AAA (https://example.test/b...)`), strings.Index(addHTML, `Example (https://example.test/a...)`); first < 0 || second <= first {
		t.Fatalf("bookmark options are not ordered by date_added descending: %d %d", first, second)
	}
	for _, want := range []string{`title="Change selected bookmark"`, `aria-disabled="true" title="Change selected bookmark"`, `title="Add another bookmark"`, `href="/admin/bookmarks/bookmark/add/?_to_field=id&amp;_popup=1"`, `title="View selected bookmark"`, `class="vCheckboxLabel" for="id_gzip">Gzip</label>`, `name="_save"`, `name="_addanother"`, `name="_continue"`} {
		if !strings.Contains(addHTML, want) {
			t.Errorf("asset add form missing %q", want)
		}
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF: %d", got.Code)
	}
	invalid := url.Values{}
	for name, values := range form {
		invalid[name] = append([]string(nil), values...)
	}
	invalid.Set("file_size", "invalid")
	if got := request(http.MethodPost, base+"add/", adminSession, invalid, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Enter a whole number") {
		t.Fatalf("invalid file size: %d %s", got.Code, got.Body.String())
	}
	invalid.Set("file_size", "")
	if got := request(http.MethodPost, base+"add/", adminSession, invalid, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "File size is required") {
		t.Fatalf("missing file size: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base {
		t.Fatalf("create asset: %d %s", got.Code, got.Body.String())
	}
	var assetID, storedBookmark, storedSize int64
	var created time.Time
	var storedFile, storedType, storedContentType, storedName, storedStatus string
	var storedGzip bool
	if err := db.QueryRowContext(ctx, `SELECT id,date_created,bookmark_id,file,file_size,asset_type,content_type,display_name,status,gzip FROM bookmarks_bookmarkasset`).Scan(&assetID, &created, &storedBookmark, &storedFile, &storedSize, &storedType, &storedContentType, &storedName, &storedStatus, &storedGzip); err != nil {
		t.Fatal(err)
	}
	if storedBookmark != bookmarkID || storedFile != "stored.html" || storedSize != 9 || storedType != "snapshot" || storedContentType != "text/html" || storedName != "Snapshot" || storedStatus != "complete" || !storedGzip || created.IsZero() {
		t.Fatalf("stored asset: bookmark=%d file=%q size=%d type=%q content=%q name=%q status=%q gzip=%t date=%v", storedBookmark, storedFile, storedSize, storedType, storedContentType, storedName, storedStatus, storedGzip, created)
	}
	changePath := base + strconv.FormatInt(assetID, 10) + "/change/"
	changePage := request(http.MethodGet, changePath, adminSession, nil, false)
	if changePage.Code != http.StatusOK || !strings.Contains(changePage.Body.String(), `href="/admin/bookmarks/bookmark/`+strconv.FormatInt(bookmarkID, 10)+`/change/?_to_field=id&amp;_popup=1"`) {
		t.Fatalf("asset change form missing selected bookmark link: %d", changePage.Code)
	}
	if got := request(http.MethodGet, changePath, viewerSession, nil, false); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "View bookmark asset") {
		t.Fatalf("viewer form: %d %s", got.Code, got.Body.String())
	}
	form.Set("display_name", "Renamed")
	form.Set("status", "failure")
	form.Del("gzip")
	if got := request(http.MethodPost, changePath, viewerSession, form, true); got.Code != http.StatusForbidden {
		t.Fatalf("viewer save: %d", got.Code)
	}
	if got := request(http.MethodPost, changePath, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("change asset: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT display_name,status,gzip FROM bookmarks_bookmarkasset WHERE id=?`, assetID).Scan(&storedName, &storedStatus, &storedGzip); err != nil || storedName != "Renamed" || storedStatus != "failure" || storedGzip {
		t.Fatalf("changed asset: %q %q %t %v", storedName, storedStatus, storedGzip, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id=? WHERE id=?`, assetID, bookmarkID); err != nil {
		t.Fatal(err)
	}
	deletePath := base + strconv.FormatInt(assetID, 10) + "/delete/"
	if got := request(http.MethodGet, deletePath, viewerSession, nil, false); got.Code != http.StatusForbidden {
		t.Fatalf("viewer delete: %d", got.Code)
	}
	if got := request(http.MethodGet, deletePath, adminSession, nil, false); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Renamed") {
		t.Fatalf("delete confirmation: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, deletePath, adminSession, url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}, true); got.Code != http.StatusFound {
		t.Fatalf("delete asset: %d %s", got.Code, got.Body.String())
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmarkasset WHERE id=?`, assetID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("asset remains: %d %v", remaining, err)
	}
	var latest sql.NullInt64
	var modified time.Time
	if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id,date_modified FROM bookmarks_bookmark WHERE id=?`, bookmarkID).Scan(&latest, &modified); err != nil || latest.Valid || !modified.Equal(added) {
		t.Fatalf("bookmark changed incorrectly: latest=%v modified=%v err=%v", latest, modified, err)
	}
	if _, err := os.Stat(assetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset file remains: %v", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT action_flag,object_repr,change_message FROM django_admin_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var gotLogs []struct {
		flag    int
		repr    string
		message string
	}
	for rows.Next() {
		var entry struct {
			flag    int
			repr    string
			message string
		}
		if err := rows.Scan(&entry.flag, &entry.repr, &entry.message); err != nil {
			t.Fatal(err)
		}
		gotLogs = append(gotLogs, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(gotLogs) != 3 || gotLogs[0].flag != 1 || gotLogs[0].repr != "Snapshot" || gotLogs[0].message != adminAdditionMessage || gotLogs[1].flag != 2 || gotLogs[1].repr != "Renamed" || gotLogs[1].message != adminChangeMessage([]string{"File size", "Display name", "Status", "Gzip"}) || gotLogs[2].flag != 3 || gotLogs[2].repr != "Renamed" || gotLogs[2].message != "" {
		t.Fatalf("asset admin logs: %+v", gotLogs)
	}
	rows.Close()
	form.Set("file", "another.html")
	form.Set("display_name", "Another")
	form.Set("_addanother", "Save and add another")
	if got := request(http.MethodPost, base+"add/", adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base+"add/" {
		t.Fatalf("save and add another redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	form.Del("_addanother")
	form.Set("_continue", "Save and continue editing")
	continueResult := request(http.MethodPost, base+"add/", adminSession, form, true)
	continuePath := continueResult.Header().Get("Location")
	if continueResult.Code != http.StatusFound || !strings.HasPrefix(continuePath, base) || !strings.HasSuffix(continuePath, "/change/") {
		t.Fatalf("save and continue redirect: %d %q", continueResult.Code, continuePath)
	}
	if got := request(http.MethodPost, continuePath, adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != continuePath {
		t.Fatalf("change and continue redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	form.Del("_continue")
	form.Set("_addanother", "Save and add another")
	if got := request(http.MethodPost, continuePath, adminSession, form, true); got.Code != http.StatusFound || got.Header().Get("Location") != base+"add/" {
		t.Fatalf("change and add another redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
}

func TestAdminBookmarkAssetPostgres(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("asset_admin_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM django_admin_log WHERE user_id = $1`,
			`DELETE FROM bookmarks_bookmark WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, admin.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	var bookmarkID int64
	added := time.Now().UTC()
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test/pg-asset','https://example.test/pg-asset','Example','','',NULL,NULL,false,false,false,$1,$2,$3,'','','') RETURNING id`, added, added, admin.ID).Scan(&bookmarkID); err != nil {
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
	pgCfg := config.Config{DBEngine: "postgres", DataDir: t.TempDir()}
	handler := New(db, pgCfg, t.TempDir())
	request := func(path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/admin/bookmarks/bookmarkasset/"
	form := url.Values{"bookmark": {strconv.FormatInt(bookmarkID, 10)}, "file": {"missing.txt"}, "file_size": {"17"}, "asset_type": {"upload"}, "content_type": {"text/plain"}, "display_name": {""}, "status": {"pending"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(base+"add/", form); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL add: %d %s", got.Code, got.Body.String())
	}
	var assetID, size int64
	if err := db.QueryRowContext(ctx, `SELECT id,file_size FROM bookmarks_bookmarkasset WHERE bookmark_id=$1`, bookmarkID).Scan(&assetID, &size); err != nil || size != 17 {
		t.Fatalf("PostgreSQL asset: id=%d size=%d err=%v", assetID, size, err)
	}
	listRequest := httptest.NewRequest(http.MethodGet, base+"?q=missing.txt&status=pending", nil)
	listRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), fmt.Sprintf("Bookmark Asset #%d", assetID)) {
		t.Fatalf("PostgreSQL filtered list: %d %s", listResponse.Code, listResponse.Body.String())
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id=$1 WHERE id=$2`, assetID, bookmarkID); err != nil {
		t.Fatal(err)
	}
	if got := request(base+strconv.FormatInt(assetID, 10)+"/delete/", url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL delete: %d %s", got.Code, got.Body.String())
	}
	var latest sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id=$1`, bookmarkID).Scan(&latest); err != nil || latest.Valid {
		t.Fatalf("PostgreSQL latest snapshot: %v %v", latest, err)
	}
}
