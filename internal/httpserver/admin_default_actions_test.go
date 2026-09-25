package httpserver

import (
	"context"
	"database/sql"
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
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminDefaultDeleteSelectedPostgres(t *testing.T) {
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
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	users := auth.NewRepository(db, "postgres")
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "bulk_admin_" + suffix, Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "bulk_owner_" + suffix, Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	otherOwner, err := users.CreateUser(ctx, auth.NewUser{Username: "bulk_other_" + suffix, Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	feedOtherOwner, err := users.CreateUser(ctx, auth.NewUser{Username: "bulk_feed_other_" + suffix, Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	feedMatch, feedOther := "feed-needle-match-"+suffix, "feed-needle-other-"+suffix
	t.Cleanup(func() {
		for _, id := range []int64{admin.ID, owner.ID, otherOwner.ID, feedOtherOwner.ID} {
			if _, err := db.ExecContext(context.Background(), `DELETE FROM django_admin_log WHERE user_id=$1`, id); err != nil {
				t.Errorf("delete admin log for %d: %v", id, err)
			}
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_feedtoken WHERE key LIKE $1`, "feed-needle-%-"+suffix); err != nil {
			t.Errorf("delete feed tokens: %v", err)
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_toast WHERE key LIKE $1`, "toast-needle-%-"+suffix); err != nil {
			t.Errorf("delete toasts: %v", err)
		}
		for _, id := range []int64{admin.ID, owner.ID, otherOwner.ID, feedOtherOwner.ID} {
			if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_userprofile WHERE user_id=$1`, id); err != nil {
				t.Errorf("delete profile for %d: %v", id, err)
			}
			if _, err := db.ExecContext(context.Background(), `DELETE FROM auth_user WHERE id=$1`, id); err != nil {
				t.Errorf("delete user %d: %v", id, err)
			}
		}
	})
	now := time.Now().UTC()
	for _, item := range []struct {
		key  string
		user int64
	}{{feedMatch, owner.ID}, {feedOther, feedOtherOwner.ID}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_feedtoken(key,created,user_id) VALUES ($1,$2,$3)`, item.key, now, item.user); err != nil {
			t.Fatal(err)
		}
	}
	toastMatch, toastOther, toastWrongOwner := "toast-needle-match-"+suffix, "toast-needle-other-"+suffix, "toast-needle-match-wrong-owner-"+suffix
	for _, item := range []struct {
		key   string
		owner int64
	}{{toastMatch, owner.ID}, {toastOther, owner.ID}, {toastWrongOwner, otherOwner.ID}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES ($1,'test',false,$2)`, item.key, item.owner); err != nil {
			t.Fatal(err)
		}
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
	var toastID, toastWrongOwnerID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key=$1`, toastMatch).Scan(&toastID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key=$1`, toastWrongOwner).Scan(&toastWrongOwnerID); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		model, filterParam, search, expected, excluded, posted, table, pkColumn, objectRepr string
		pk                                                                                  any
	}{
		{model: "feedtoken", filterParam: "user__username", search: "needle-match-" + suffix, expected: feedMatch, excluded: feedOther, posted: feedOther, table: "bookmarks_feedtoken", pkColumn: "key", objectRepr: feedMatch, pk: feedMatch},
		{model: "toast", filterParam: "owner__username", search: "needle-match-" + suffix, expected: fmt.Sprintf("Toast object (%d)", toastID), excluded: toastWrongOwner, posted: strconv.FormatInt(toastWrongOwnerID, 10), table: "bookmarks_toast", pkColumn: "id", objectRepr: fmt.Sprintf("Toast object (%d)", toastID), pk: toastID},
	} {
		path := "/admin/bookmarks/" + item.model + "/?" + url.Values{"q": {item.search}, item.filterParam: {owner.Username}}.Encode()
		form := url.Values{"action": {"delete_selected"}, "select_across": {"1"}, "_selected_action": {item.posted}, "csrfmiddlewaretoken": {csrf}}
		confirm := request(path, form)
		if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), item.expected) {
			t.Fatalf("PostgreSQL %s filtered confirmation: %d %s", item.model, confirm.Code, confirm.Body.String())
		}
		if item.model == "toast" {
			if strings.Contains(confirm.Body.String(), toastWrongOwner) || strings.Contains(confirm.Body.String(), toastOther) {
				t.Fatalf("PostgreSQL toast confirmation included rows outside search/owner filter: %s", confirm.Body.String())
			}
		}
		form.Set("post", "yes")
		if got := request(path, form); got.Code != http.StatusFound {
			t.Fatalf("PostgreSQL %s bulk delete: %d %s", item.model, got.Code, got.Body.String())
		}
		var count int
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s=$1`, item.table, item.pkColumn), item.pk).Scan(&count); err != nil || count != 0 {
			t.Fatalf("PostgreSQL %s target count=%d err=%v", item.model, count, err)
		}
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s=$1`, item.table, map[string]string{"feedtoken": "key", "toast": "key"}[item.model]), item.excluded).Scan(&count); err != nil || count != 1 {
			t.Fatalf("PostgreSQL %s excluded row count=%d err=%v", item.model, count, err)
		}
		if item.model == "toast" {
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_toast WHERE key=$1`, toastOther).Scan(&count); err != nil || count != 1 {
				t.Fatalf("PostgreSQL toast outside search count=%d err=%v", count, err)
			}
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM django_admin_log WHERE user_id=$1 AND action_flag=3 AND object_repr=$2`, admin.ID, item.objectRepr).Scan(&count); err != nil || count != 1 {
			t.Fatalf("PostgreSQL %s delete log count=%d err=%v", item.model, count, err)
		}
	}
}

func TestAdminDefaultDeleteSelectedFilteredAcross(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "default-action-admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "default-action-owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	feedOwner, err := users.CreateUser(ctx, auth.NewUser{Username: "default-action-feed-owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "default-action-viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, viewer.ID, "bookmarks", "bookmarkasset", "view_bookmarkasset")
	viewerSession, err := users.CreateSession(ctx, viewer.ID, time.Hour)
	if err != nil {
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
	handler := New(db, cfg, t.TempDir())
	now := time.Now().UTC()
	models := []struct {
		model, path, matchingRepr string
		matchingID, otherID       string
		matchingCount, otherCount string
	}{
		{model: "bookmarkasset", path: "/admin/bookmarks/bookmarkasset/", matchingRepr: "asset-match", matchingCount: `SELECT COUNT(*) FROM bookmarks_bookmarkasset WHERE display_name='asset-match'`, otherCount: `SELECT COUNT(*) FROM bookmarks_bookmarkasset WHERE display_name='asset-other'`},
		{model: "bookmarkbundle", path: "/admin/bookmarks/bookmarkbundle/", matchingRepr: "bundle-match", matchingCount: `SELECT COUNT(*) FROM bookmarks_bookmarkbundle WHERE name='bundle-match'`, otherCount: `SELECT COUNT(*) FROM bookmarks_bookmarkbundle WHERE name='bundle-other'`},
		{model: "toast", path: "/admin/bookmarks/toast/", matchingCount: `SELECT COUNT(*) FROM bookmarks_toast WHERE key='toast-match'`, otherCount: `SELECT COUNT(*) FROM bookmarks_toast WHERE key='toast-other'`},
		{model: "apitoken", path: "/admin/bookmarks/apitoken/", matchingRepr: "api-match (default-action-owner)", matchingCount: `SELECT COUNT(*) FROM bookmarks_apitoken WHERE name='api-match'`, otherCount: `SELECT COUNT(*) FROM bookmarks_apitoken WHERE name='api-other'`},
		{model: "feedtoken", path: "/admin/bookmarks/feedtoken/", matchingRepr: "feed-match", matchingCount: `SELECT COUNT(*) FROM bookmarks_feedtoken WHERE key='feed-match'`, otherCount: `SELECT COUNT(*) FROM bookmarks_feedtoken WHERE key='feed-other'`},
	}
	bookmark, _, err := bookmarks.NewRepository(db, cfg.DBEngine).CreateOrUpdateData(ctx, owner.ID, bookmarks.CreateInput{URL: "https://example.com/admin-default-action"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []struct{ name, file string }{{"asset-match", "admin-actions/match.html"}, {"asset-other", "admin-actions/other.html"}} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets", filepath.Dir(asset.file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.DataDir, "assets", asset.file), []byte(asset.name), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES (?,?,1,'snapshot','text/html',?,'complete',0,?)`, now, asset.file, asset.name, bookmark.ID); err != nil {
			t.Fatal(err)
		}
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkasset WHERE display_name='asset-match'`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id=? WHERE id=?`, assetID, bookmark.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle-match", "bundle-other"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,"order",date_created,date_modified,owner_id,filter_shared,filter_unread) VALUES (?,'','','','',0,?,?,?,'off','off')`, name, now, now, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"toast-match", "toast-other"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES (?,'test',0,?)`, key, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"api-match", "api-other"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken(key,name,created,user_id) VALUES (?,?,?,?)`, name+"-key", name, now, owner.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i, key := range []string{"feed-match", "feed-other"} {
		feedUser := owner.ID
		if i == 1 {
			feedUser = feedOwner.ID
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_feedtoken(key,created,user_id) VALUES (?,?,?)`, key, now, feedUser); err != nil {
			t.Fatal(err)
		}
	}

	requestAs := func(method, path, userSession string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		body := strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: userSession})
		if withCSRF {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	request := func(method, path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		return requestAs(method, path, session, form, withCSRF)
	}
	viewerList := requestAs(http.MethodGet, "/admin/bookmarks/bookmarkasset/", viewerSession, nil, false)
	if viewerList.Code != http.StatusOK || strings.Contains(viewerList.Body.String(), "Delete selected") {
		t.Fatalf("view-only user should not see bulk delete: %d %s", viewerList.Code, viewerList.Body.String())
	}
	viewerForm := url.Values{"action": {"delete_selected"}, "select_across": {"0"}, "_selected_action": {"1"}, "csrfmiddlewaretoken": {csrf}}
	if got := requestAs(http.MethodPost, "/admin/bookmarks/bookmarkasset/", viewerSession, viewerForm, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only bulk delete should be forbidden: %d", got.Code)
	}
	for i := range models {
		item := &models[i]
		query := "match"
		if item.model == "bookmarkasset" {
			query = "asset-match"
		}
		path := item.path + "?q=" + url.QueryEscape(query)
		list := request(http.MethodGet, path, nil, false)
		if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Delete selected") || !strings.Contains(list.Body.String(), `name="_selected_action"`) {
			t.Fatalf("%s action missing from list: %d %s", item.model, list.Code, list.Body.String())
		}
		var matchID, otherID string
		switch item.model {
		case "bookmarkasset":
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkasset WHERE display_name='asset-match'`).Scan(&matchID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkasset WHERE display_name='asset-other'`).Scan(&otherID); err != nil {
				t.Fatal(err)
			}
		case "bookmarkbundle":
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkbundle WHERE name='bundle-match'`).Scan(&matchID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkbundle WHERE name='bundle-other'`).Scan(&otherID); err != nil {
				t.Fatal(err)
			}
		case "toast":
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key='toast-match'`).Scan(&matchID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key='toast-other'`).Scan(&otherID); err != nil {
				t.Fatal(err)
			}
			item.matchingRepr = "Toast object (" + matchID + ")"
		case "apitoken":
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_apitoken WHERE name='api-match'`).Scan(&matchID); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_apitoken WHERE name='api-other'`).Scan(&otherID); err != nil {
				t.Fatal(err)
			}
		case "feedtoken":
			matchID, otherID = "feed-match", "feed-other"
		}
		if matchID == "" || otherID == "" {
			t.Fatalf("%s fixture ids missing: match=%q other=%q", item.model, matchID, otherID)
		}
		form := url.Values{"action": {"delete_selected"}, "select_across": {"1"}, "_selected_action": {otherID}, "csrfmiddlewaretoken": {csrf}}
		if got := request(http.MethodPost, path, form, false); got.Code != http.StatusForbidden {
			t.Fatalf("%s bulk delete without CSRF: %d", item.model, got.Code)
		}
		confirm := request(http.MethodPost, path, form, true)
		if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), item.matchingRepr) || strings.Contains(confirm.Body.String(), "other") {
			t.Fatalf("%s filtered select_across confirmation: %d %s", item.model, confirm.Code, confirm.Body.String())
		}
		form.Set("post", "yes")
		if got := request(http.MethodPost, path, form, true); got.Code != http.StatusFound {
			t.Fatalf("%s bulk delete: %d %s", item.model, got.Code, got.Body.String())
		}
		var count int
		if err := db.QueryRowContext(ctx, item.matchingCount).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s matching row count=%d err=%v", item.model, count, err)
		}
		if err := db.QueryRowContext(ctx, item.otherCount).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s nonmatching row count=%d err=%v", item.model, count, err)
		}
		if item.model == "bookmarkasset" {
			if _, err := os.Stat(filepath.Join(cfg.DataDir, "assets", "admin-actions/match.html")); !os.IsNotExist(err) {
				t.Fatalf("deleted asset file still exists (stat err=%v)", err)
			}
			if _, err := os.Stat(filepath.Join(cfg.DataDir, "assets", "admin-actions/other.html")); err != nil {
				t.Fatalf("unselected asset file missing: %v", err)
			}
			var latest any
			if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id=?`, bookmark.ID).Scan(&latest); err != nil || latest != nil {
				t.Fatalf("latest snapshot was not cleared: %#v err=%v", latest, err)
			}
		}
		var logs int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM django_admin_log WHERE user_id=? AND action_flag=3 AND object_repr=?`, admin.ID, item.matchingRepr).Scan(&logs); err != nil || logs != 1 {
			t.Fatalf("%s delete log count=%d err=%v", item.model, logs, err)
		}
		if item.model != "feedtoken" {
			if _, err := strconv.ParseInt(matchID, 10, 64); err != nil {
				t.Fatalf("%s id is not numeric: %q", item.model, matchID)
			}
		}
	}
}
