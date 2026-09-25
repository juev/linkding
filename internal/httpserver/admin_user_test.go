package httpserver

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
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
	"github.com/juev/linkding/internal/settings"
	"github.com/juev/linkding/internal/store"
)

func adminUserProfileValues(t *testing.T, db *sql.DB, engine string, id int64) url.Values {
	t.Helper()
	profile, err := settings.LoadProfileForm(context.Background(), db, engine, id)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"profile-TOTAL_FORMS": {"1"}, "profile-INITIAL_FORMS": {"1"}}
	for _, field := range settings.ProfileFields {
		if value := profile.Get(field.Name); value != "" {
			form.Set("profile-0-"+field.Name, value)
		}
	}
	return form
}

func TestAdminUserAddAndChange(t *testing.T) {
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
	users := auth.NewRepository(db, cfg.DBEngine)
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`INSERT INTO django_content_type(id,app_label,model) VALUES (801,'auth','user')`, `INSERT INTO auth_permission(id,name,content_type_id,codename) VALUES (801,'Can view user',801,'view_user')`, `INSERT INTO auth_permission(id,name,content_type_id,codename) VALUES (802,'Can change user',801,'change_user')`, `INSERT INTO auth_group(id,name) VALUES (801,'Editors')`} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_user_user_permissions(user_id,permission_id) VALUES (?,801)`, viewer.ID); err != nil {
		t.Fatal(err)
	}
	adminSession, err := users.CreateSession(ctx, admin.ID, time.Hour)
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
	base := "/admin/auth/user/"
	addPage := request(http.MethodGet, base+"add/", adminSession, nil, true)
	if addPage.Code != http.StatusOK || !strings.Contains(addPage.Body.String(), `name="usable_password"`) || strings.Contains(addPage.Body.String(), `profile-TOTAL_FORMS`) {
		t.Fatalf("add page: %d %s", addPage.Code, addPage.Body.String())
	}
	if got := request(http.MethodGet, base+"add/", viewerSession, nil, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only add: %d", got.Code)
	}
	addForm := url.Values{"username": {"newuser"}, "usable_password": {"true"}, "password1": {"short"}, "password2": {"short"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, base+"add/", adminSession, addForm, false); got.Code != http.StatusForbidden {
		t.Fatalf("add without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, base+"add/", adminSession, addForm, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "too short") {
		t.Fatalf("short password validation: %d %s", got.Code, got.Body.String())
	}
	addForm.Set("usable_password", "false")
	addForm.Del("password1")
	addForm.Del("password2")
	if got := request(http.MethodPost, base+"add/", adminSession, addForm, true); got.Code != http.StatusFound {
		t.Fatalf("add without password: %d %s", got.Code, got.Body.String())
	}
	var id int64
	var password string
	if err := db.QueryRowContext(ctx, `SELECT id,password FROM auth_user WHERE username='newuser'`).Scan(&id, &password); err != nil || !strings.HasPrefix(password, "!") || len(password) != 41 {
		t.Fatalf("unusable password: id=%d password=%q err=%v", id, password, err)
	}
	var profileCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_userprofile WHERE user_id=?`, id).Scan(&profileCount); err != nil || profileCount != 1 {
		t.Fatalf("profile after add: %d %v", profileCount, err)
	}
	addForm.Set("username", "passworduser")
	addForm.Set("usable_password", "true")
	addForm.Set("password1", "StrongRandomPass-2026")
	addForm.Set("password2", "StrongRandomPass-2026")
	if got := request(http.MethodPost, base+"add/", adminSession, addForm, true); got.Code != http.StatusFound {
		t.Fatalf("add with password: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE username='passworduser'`).Scan(&password); err != nil {
		t.Fatal(err)
	}
	if correct, err := auth.VerifyPassword("StrongRandomPass-2026", password); err != nil || !correct {
		t.Fatalf("password hash unusable: correct=%t err=%v", correct, err)
	}
	changePath := base + strconv.FormatInt(id, 10) + "/change/"
	view := request(http.MethodGet, changePath, viewerSession, nil, true)
	if view.Code != http.StatusOK || strings.Contains(view.Body.String(), `value="Save"`) || !strings.Contains(view.Body.String(), `profile-0-theme`) {
		t.Fatalf("view-only change page: %d %s", view.Code, view.Body.String())
	}
	changeForm := adminUserProfileValues(t, db, cfg.DBEngine, id)
	changeForm.Set("username", "renameduser")
	changeForm.Set("first_name", "Alice")
	changeForm.Set("last_name", "Example")
	changeForm.Set("email", "alice@example.test")
	changeForm.Set("is_active", "on")
	changeForm.Set("is_staff", "on")
	changeForm.Set("date_joined_0", "2020-01-02")
	changeForm.Set("date_joined_1", "03:04:05")
	changeForm.Set("groups", "801")
	changeForm.Set("user_permissions", "802")
	changeForm.Set("profile-0-theme", "dark")
	changeForm.Set("profile-0-enable_sharing", "on")
	changeForm.Set("profile-0-items_per_page", "50")
	changeForm.Set("profile-0-custom_css", "body { color: red; }")
	changeForm.Set("profile-0-custom_css_hash", "wrong")
	changeForm.Set("csrfmiddlewaretoken", csrf)
	if got := request(http.MethodPost, changePath, viewerSession, changeForm, true); got.Code != http.StatusForbidden {
		t.Fatalf("view-only POST: %d", got.Code)
	}
	if got := request(http.MethodPost, changePath, adminSession, changeForm, true); got.Code != http.StatusFound {
		t.Fatalf("change: %d %s", got.Code, got.Body.String())
	}
	var username, email, firstName, lastName, theme, cssHash string
	var staff bool
	var joined time.Time
	if err := db.QueryRowContext(ctx, `SELECT username,email,first_name,last_name,is_staff,date_joined FROM auth_user WHERE id=?`, id).Scan(&username, &email, &firstName, &lastName, &staff, &joined); err != nil || username != "renameduser" || email != "alice@example.test" || firstName != "Alice" || lastName != "Example" || !staff || joined.UTC().Hour() != 3 {
		t.Fatalf("user change: name=%q email=%q first=%q last=%q staff=%t joined=%v err=%v", username, email, firstName, lastName, staff, joined, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT theme,custom_css_hash FROM bookmarks_userprofile WHERE user_id=?`, id).Scan(&theme, &cssHash); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("body { color: red; }"))
	if theme != "dark" || cssHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("profile change: theme=%q hash=%q", theme, cssHash)
	}
	for _, relation := range []struct {
		query string
		id    int64
	}{{`SELECT COUNT(*) FROM auth_user_groups WHERE user_id=? AND group_id=?`, 801}, {`SELECT COUNT(*) FROM auth_user_user_permissions WHERE user_id=? AND permission_id=?`, 802}} {
		if err := db.QueryRowContext(ctx, relation.query, id, relation.id).Scan(&profileCount); err != nil || profileCount != 1 {
			t.Fatalf("missing relation: %d %v", profileCount, err)
		}
	}
	changeForm.Set("username", "rolledback")
	changeForm.Set("profile-0-items_per_page", "3")
	if got := request(http.MethodPost, changePath, adminSession, changeForm, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Invalid profile field") {
		t.Fatalf("invalid profile: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT username FROM auth_user WHERE id=?`, id).Scan(&username); err != nil || username != "renameduser" {
		t.Fatalf("user update escaped failed profile transaction: %q %v", username, err)
	}
}

func TestAdminUserPostgres(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("admin_user_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM auth_user_user_permissions WHERE user_id IN (SELECT id FROM auth_user WHERE id=$1 OR username='postgres_new_user')`, `DELETE FROM auth_user_groups WHERE user_id IN (SELECT id FROM auth_user WHERE id=$1 OR username='postgres_new_user')`, `DELETE FROM bookmarks_userprofile WHERE user_id IN (SELECT id FROM auth_user WHERE id=$1 OR username='postgres_new_user')`, `DELETE FROM auth_user WHERE id=$1 OR username='postgres_new_user'`} {
			if _, err := db.ExecContext(context.Background(), query, admin.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, config.Config{DBEngine: "postgres", DataDir: t.TempDir()}, t.TempDir())
	request := func(path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/admin/auth/user/"
	form := url.Values{"username": {"postgres_new_user"}, "usable_password": {"false"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(base+"add/", form); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL add: %d %s", got.Code, got.Body.String())
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='postgres_new_user'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	change := adminUserProfileValues(t, db, "postgres", id)
	change.Set("username", "postgres_new_user")
	change.Set("first_name", "PG")
	change.Set("is_active", "on")
	change.Set("date_joined_0", "2020-01-02")
	change.Set("date_joined_1", "03:04:05")
	change.Set("profile-0-theme", "dark")
	change.Set("csrfmiddlewaretoken", csrf)
	if got := request(base+strconv.FormatInt(id, 10)+"/change/", change); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL change: %d %s", got.Code, got.Body.String())
	}
	var firstName, theme string
	if err := db.QueryRowContext(ctx, `SELECT first_name FROM auth_user WHERE id=$1`, id).Scan(&firstName); err != nil || firstName != "PG" {
		t.Fatalf("PostgreSQL first name: %q %v", firstName, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT theme FROM bookmarks_userprofile WHERE user_id=$1`, id).Scan(&theme); err != nil || theme != "dark" {
		t.Fatalf("PostgreSQL profile theme: %q %v", theme, err)
	}
	var bookmarkID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES ('https://example.test/pg-user','https://example.test/pg-user','','','',NULL,NULL,false,false,false,$1,$2,$3,'','','') RETURNING id`, time.Now().UTC(), time.Now().UTC(), id).Scan(&bookmarkID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES ($1,'',0,'snapshot','text/html','Snapshot','complete',false,$2)`, time.Now().UTC(), bookmarkID); err != nil {
		t.Fatal(err)
	}
	if got := request(base+strconv.FormatInt(id, 10)+"/delete/", url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}); got.Code != http.StatusFound {
		t.Fatalf("PostgreSQL delete: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_user WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("PostgreSQL user remains: %d %v", count, err)
	}
}

func TestAdminUserPasswordChange(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "old-admin-pass", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	target, err := users.CreateUser(ctx, auth.NewUser{Username: "target", Password: "old-target-pass"})
	if err != nil {
		t.Fatal(err)
	}
	adminSession, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	targetSession, err := users.CreateSession(ctx, target.ID, time.Hour)
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
	path := "/admin/auth/user/" + strconv.FormatInt(target.ID, 10) + "/password/"
	page := request(http.MethodGet, path, adminSession, nil, true)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="password1"`) || !strings.Contains(page.Body.String(), `name="usable_password"`) {
		t.Fatalf("password page: %d %s", page.Code, page.Body.String())
	}
	form := url.Values{"usable_password": {"true"}, "password1": {"short"}, "password2": {"short"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, path, adminSession, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("password POST without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "too short") {
		t.Fatalf("short password: %d %s", got.Code, got.Body.String())
	}
	form.Set("password1", "AnotherStrongPass-2026")
	form.Set("password2", "AnotherStrongPass-2026")
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("change target password: %d %s", got.Code, got.Body.String())
	}
	if _, err := users.AuthenticateSession(ctx, targetSession); err == nil {
		t.Fatal("old target session survived password change")
	}
	var encoded string
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE id=?`, target.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if correct, err := auth.VerifyPassword("AnotherStrongPass-2026", encoded); err != nil || !correct {
		t.Fatalf("new password: correct=%t err=%v", correct, err)
	}
	form.Set("usable_password", "false")
	form.Del("password1")
	form.Del("password2")
	if got := request(http.MethodPost, path, adminSession, form, true); got.Code != http.StatusFound {
		t.Fatalf("disable password: %d %s", got.Code, got.Body.String())
	}
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE id=?`, target.ID).Scan(&encoded); err != nil || !strings.HasPrefix(encoded, "!") || len(encoded) != 41 {
		t.Fatalf("disabled password: %q %v", encoded, err)
	}
	selfPath := "/admin/auth/user/" + strconv.FormatInt(admin.ID, 10) + "/password/"
	form.Set("usable_password", "true")
	form.Set("password1", "AdminStrongPass-2026")
	form.Set("password2", "AdminStrongPass-2026")
	self := request(http.MethodPost, selfPath, adminSession, form, true)
	if self.Code != http.StatusFound {
		t.Fatalf("change own password: %d %s", self.Code, self.Body.String())
	}
	var newSession string
	for _, cookie := range self.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			newSession = cookie.Value
		}
	}
	if newSession == "" {
		t.Fatal("new session cookie missing after own password change")
	}
	if _, err := users.AuthenticateSession(ctx, newSession); err != nil {
		t.Fatalf("new session rejected: %v", err)
	}
	if _, err := users.AuthenticateSession(ctx, adminSession); err == nil {
		t.Fatal("old session survived own password change")
	}
}

func TestAdminUserDeleteCascade(t *testing.T) {
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
	target, err := users.CreateUser(ctx, auth.NewUser{Username: "target", Password: "password"})
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
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_globalsettings(landing_page,guest_profile_user_id,enable_link_prefetch) VALUES ('bookmarks',?,0)`, target.ID); err != nil {
		t.Fatal(err)
	}
	var targetBookmark, otherBookmark, tagID, assetID int64
	added := time.Now().UTC()
	for _, item := range []struct {
		url     string
		ownerID int64
		id      *int64
	}{{"https://example.test/target", target.ID, &targetBookmark}, {"https://example.test/other", admin.ID, &otherBookmark}} {
		result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file) VALUES (?,?,'','','',NULL,NULL,0,0,0,?,?,?,'','',?)`, item.url, item.url, added, added, item.ownerID, map[bool]string{true: "preview.png", false: ""}[item.ownerID == target.ID])
		if err != nil {
			t.Fatal(err)
		}
		*item.id, err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES ('cross-owner',?,?)`, added, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	tagID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) VALUES (?,?)`, otherBookmark, tagID); err != nil {
		t.Fatal(err)
	}
	result, err = db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkasset(date_created,file,file_size,asset_type,content_type,display_name,status,gzip,bookmark_id) VALUES (?,'snapshot.html',4,'snapshot','text/html','Snapshot','complete',0,?)`, added, targetBookmark)
	if err != nil {
		t.Fatal(err)
	}
	assetID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id=? WHERE id=?`, assetID, otherBookmark); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken(key,name,created,user_id) VALUES ('target-token','Target',?,?)`, added, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_feedtoken(key,created,user_id) VALUES ('feed-token',?,?)`, added, target.ID); err != nil {
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
	path := "/admin/auth/user/" + strconv.FormatInt(target.ID, 10) + "/delete/"
	request := func(method string, form url.Values, csrfCookie bool) *httptest.ResponseRecorder {
		body := strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: adminSession})
		if csrfCookie {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, nil, true); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "target") {
		t.Fatalf("delete confirmation: %d %s", got.Code, got.Body.String())
	}
	form := url.Values{"post": {"yes"}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("delete without CSRF: %d", got.Code)
	}
	if got := request(http.MethodPost, form, true); got.Code != http.StatusFound {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	for _, query := range []string{`SELECT COUNT(*) FROM auth_user WHERE id=?`, `SELECT COUNT(*) FROM bookmarks_bookmark WHERE owner_id=?`, `SELECT COUNT(*) FROM bookmarks_tag WHERE owner_id=?`, `SELECT COUNT(*) FROM bookmarks_userprofile WHERE user_id=?`, `SELECT COUNT(*) FROM bookmarks_apitoken WHERE user_id=?`, `SELECT COUNT(*) FROM bookmarks_feedtoken WHERE user_id=?`} {
		var count int
		if err := db.QueryRowContext(ctx, query, target.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deleted user data remains (%s): %d %v", query, count, err)
		}
	}
	var snapshot, guest sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id=?`, otherBookmark).Scan(&snapshot); err != nil || snapshot.Valid {
		t.Fatalf("other bookmark snapshot: %v %v", snapshot, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT guest_profile_user_id FROM bookmarks_globalsettings LIMIT 1`).Scan(&guest); err != nil || guest.Valid {
		t.Fatalf("guest profile reference: %v %v", guest, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_bookmark_tags WHERE bookmark_id=?`, otherBookmark).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-owner tag relation: %d %v", count, err)
	}
	for _, item := range []struct{ directory, name string }{{"assets", "snapshot.html"}, {"previews", "preview.png"}} {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, item.directory, item.name)); !os.IsNotExist(err) {
			t.Fatalf("deleted file remains: %s/%s: %v", item.directory, item.name, err)
		}
	}
}
