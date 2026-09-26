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

func TestAdminUserBulkDeleteFiltered(t *testing.T) {
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
	match, err := users.CreateUser(ctx, auth.NewUser{Username: "match-user", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateUser(ctx, auth.NewUser{Username: "other-user", Password: "password"})
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
	path := "/admin/auth/user/?q=match&is_active__exact=1"
	request := func(method string, form url.Values, csrfCookie bool) *httptest.ResponseRecorder {
		body := strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		if csrfCookie {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	list := request(http.MethodGet, nil, true)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `Delete selected users`) || strings.Contains(list.Body.String(), `Delete selected bookmarks`) || !strings.Contains(list.Body.String(), `name="_selected_action"`) {
		t.Fatalf("user action list: %d %s", list.Code, list.Body.String())
	}
	form := url.Values{"action": {"delete_selected"}, "select_across": {"1"}, "_selected_action": {strconv.FormatInt(other.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	if got := request(http.MethodPost, form, false); got.Code != http.StatusForbidden {
		t.Fatalf("action without CSRF: %d", got.Code)
	}
	confirm := request(http.MethodPost, form, true)
	if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), "match-user") || strings.Contains(confirm.Body.String(), `other-user`) || !strings.Contains(confirm.Body.String(), "<h1>Delete multiple objects</h1>") || !strings.Contains(confirm.Body.String(), `href="/admin/auth/user/`+strconv.FormatInt(match.ID, 10)+`/change/">match-user</a>`) || !strings.Contains(confirm.Body.String(), `Users: 1`) {
		t.Fatalf("filtered confirmation: %d %s", confirm.Code, confirm.Body.String())
	}
	form.Set("post", "yes")
	if got := request(http.MethodPost, form, true); got.Code != http.StatusFound {
		t.Fatalf("bulk delete: %d %s", got.Code, got.Body.String())
	}
	for _, item := range []struct {
		id   int64
		want int
	}{{match.ID, 0}, {other.ID, 1}, {admin.ID, 1}} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_user WHERE id=?`, item.id).Scan(&count); err != nil || count != item.want {
			t.Fatalf("user %d count=%d want=%d err=%v", item.id, count, item.want, err)
		}
	}
	var logCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM django_admin_log WHERE action_flag=3 AND object_repr='match-user'`).Scan(&logCount); err != nil || logCount != 1 {
		t.Fatalf("filtered user delete log: count=%d err=%v", logCount, err)
	}
}

func TestAdminUserBulkDeletePostgres(t *testing.T) {
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
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bulk_admin_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	match, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bulk_match_%d", time.Now().UnixNano()), Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bulk_other_%d", time.Now().UnixNano()), Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range []int64{admin.ID, match.ID, other.ID} {
			if _, err := db.ExecContext(context.Background(), `DELETE FROM django_admin_log WHERE user_id=$1`, id); err != nil {
				t.Errorf("admin log cleanup: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_userprofile WHERE user_id=$1`, id); err != nil {
				t.Errorf("profile cleanup: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), `DELETE FROM auth_user WHERE id=$1`, id); err != nil {
				t.Errorf("user cleanup: %v", err)
			}
		}
	})
	summary, nodes, err := adminUserDeletionGraph(ctx, db, "postgres", "/", []adminUserSelected{{ID: match.ID, Username: match.Username}})
	if err != nil || len(summary) == 0 || len(nodes) != 1 || nodes[0].URL != "/admin/auth/user/"+strconv.FormatInt(match.ID, 10)+"/change/" {
		t.Fatalf("PostgreSQL deletion graph: summary=%#v nodes=%#v err=%v", summary, nodes, err)
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, config.Config{DBEngine: "postgres", DataDir: t.TempDir()}, t.TempDir())
	path := "/admin/auth/user/?q=bulk_match"
	form := url.Values{"action": {"delete_selected"}, "select_across": {"0"}, "_selected_action": {strconv.FormatInt(match.ID, 10), strconv.FormatInt(other.ID, 10)}, "csrfmiddlewaretoken": {csrf}, "post": {"yes"}}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("PostgreSQL bulk delete: %d %s", w.Code, w.Body.String())
	}
	for _, item := range []struct {
		id   int64
		want int
	}{{match.ID, 0}, {other.ID, 1}} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_user WHERE id=$1`, item.id).Scan(&count); err != nil || count != item.want {
			t.Fatalf("PostgreSQL user %d count=%d want=%d err=%v", item.id, count, item.want, err)
		}
	}
}
