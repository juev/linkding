package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminOtherModelChangeAndHistory(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/", TimeZone: "UTC"}
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
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	toast, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES ('notice','Message',0,?)`, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	toastID, _ := toast.LastInsertId()
	api, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken(key,name,created,user_id) VALUES ('api-token','Fixture',?,?)`, now, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	apiID, _ := api.LastInsertId()
	const feedKey = "ffffffffffffffffffffffffffffffffffffffff"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_feedtoken(key,created,user_id) VALUES (?,?,?)`, feedKey, now, owner.ID); err != nil {
		t.Fatal(err)
	}
	bundle, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,filter_unread,filter_shared,"order",date_created,date_modified,owner_id) VALUES ('Work','','','','','off','off',0,?,?,?)`, now, now, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	bundleID, _ := bundle.LastInsertId()
	bookmark, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test/a','https://example.test/a','Example','','',NULL,NULL,0,0,0,?,?,?,'','','')`, now, now, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookmarkID, _ := bookmark.LastInsertId()
	asset, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES (?,'notes.txt',5,'upload','text/plain','notes.txt','complete',0,?)`, now, bookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	assetID, _ := asset.LastInsertId()
	handler := New(db, cfg, t.TempDir())
	request := func(method, path, session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	cases := []struct{ model, id, repr, plural string }{
		{"toast", strconv.FormatInt(toastID, 10), "Toast object (" + strconv.FormatInt(toastID, 10) + ")", "Toasts"},
		{"apitoken", strconv.FormatInt(apiID, 10), "Fixture (owner)", "Api tokens"},
		{"feedtoken", feedKey, feedKey, "Feed tokens"},
		{"bookmarkbundle", strconv.FormatInt(bundleID, 10), "Work", "Bookmark bundles"},
		{"bookmarkasset", strconv.FormatInt(assetID, 10), "notes.txt", "Bookmark assets"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			base := "/linkding/admin/bookmarks/" + tc.model + "/" + tc.id + "/"
			change := request(http.MethodGet, base+"change/", adminSession)
			if change.Code != 200 || !strings.Contains(change.Body.String(), tc.repr+" | Change") || !strings.Contains(change.Body.String(), "<h2>"+tc.repr+"</h2>") || !strings.Contains(change.Body.String(), `href="`+base+`history/"`) {
				t.Fatalf("change form: %d %s", change.Code, change.Body.String())
			}
			history := request(http.MethodGet, base+"history/", adminSession)
			if history.Code != 200 || !strings.Contains(history.Body.String(), "Change history: "+tc.repr) || !strings.Contains(history.Body.String(), tc.plural) || !strings.Contains(history.Body.String(), "doesn’t have a change history") {
				t.Fatalf("empty history: %d %s", history.Code, history.Body.String())
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeAdminLog(ctx, tx, cfg.DBEngine, admin.ID, "bookmarks", tc.model, tc.id, tc.repr, 1, adminAdditionMessage); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			history = request(http.MethodGet, base+"history/", adminSession)
			if history.Code != 200 || !strings.Contains(history.Body.String(), "Added.") || !strings.Contains(history.Body.String(), "1 entry") {
				t.Fatalf("logged history: %d %s", history.Code, history.Body.String())
			}
			if got := request(http.MethodPost, base+"history/", adminSession); got.Code != http.StatusForbidden {
				t.Fatalf("history POST without CSRF: %d", got.Code)
			}
		})
	}
	if got := request(http.MethodGet, "/linkding/admin/bookmarks/bookmarkasset/"+strconv.FormatInt(assetID, 10)+"/history/", viewerSession); got.Code != 200 {
		t.Fatalf("view permission: %d", got.Code)
	}
	if got := request(http.MethodGet, "/linkding/admin/bookmarks/apitoken/"+strconv.FormatInt(apiID, 10)+"/history/", viewerSession); got.Code != http.StatusForbidden {
		t.Fatalf("missing permission: %d", got.Code)
	}
	if got := request(http.MethodGet, "/linkding/admin/bookmarks/bookmarkasset/999999/history/", adminSession); got.Code != http.StatusNotFound {
		t.Fatalf("missing object: %d", got.Code)
	}
}

func TestAdminOtherHistoryPostgres(t *testing.T) {
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
	admin, err := auth.NewRepository(db, "postgres").CreateUser(ctx, auth.NewUser{
		Username: fmt.Sprintf("history_admin_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var toastID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES ('history-test','Notice',false,$1) RETURNING id`, admin.ID).Scan(&toastID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM django_admin_log WHERE user_id=$1`,
			`DELETE FROM bookmarks_toast WHERE owner_id=$1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id=$1`,
			`DELETE FROM auth_user WHERE id=$1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, admin.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAdminLog(ctx, tx, "postgres", admin.ID, "bookmarks", "toast", strconv.FormatInt(toastID, 10), "Toast object ("+strconv.FormatInt(toastID, 10)+")", 1, adminAdditionMessage); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/bookmarks/toast/"+strconv.FormatInt(toastID, 10)+"/history/", nil)
	serveAdminOtherHistory(w, r, config.Config{DBEngine: "postgres", TimeZone: "UTC"}, db, admin, "toast", strconv.FormatInt(toastID, 10))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Added.") || !strings.Contains(w.Body.String(), "Change history: Toast object") {
		t.Fatalf("PostgreSQL history: %d %s", w.Code, w.Body.String())
	}
	session, err := auth.NewRepository(db, "postgres").CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/admin/bookmarks/toast/?_facets=True", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	w = httptest.NewRecorder()
	New(db, config.Config{DBEngine: "postgres", TimeZone: "UTC"}, t.TempDir()).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), admin.Username+" (1)") {
		t.Fatalf("PostgreSQL facets: %d %s", w.Code, w.Body.String())
	}
}
