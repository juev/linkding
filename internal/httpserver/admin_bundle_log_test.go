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

func TestAdminBundleAuditLog(t *testing.T) {
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
	actor, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, actor.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		form.Set("csrfmiddlewaretoken", csrf)
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusFound {
			t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	base := "/admin/bookmarks/bookmarkbundle/"
	form := url.Values{"name": {"Work"}, "search": {"golang"}, "any_tags": {"code"}, "all_tags": {"work"}, "excluded_tags": {"old"}, "filter_unread": {"yes"}, "filter_shared": {"no"}, "order": {"17"}, "owner": {strconv.FormatInt(owner.ID, 10)}}
	request(base+"add/", form)
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkbundle WHERE name='Work'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	form.Set("name", "Updated")
	form.Set("search", "rust")
	form.Set("filter_unread", "no")
	request(base+strconv.FormatInt(id, 10)+"/change/", form)
	request(base+strconv.FormatInt(id, 10)+"/delete/", url.Values{"post": {"yes"}})

	rows, err := db.QueryContext(ctx, `SELECT l.action_flag,l.object_id,l.object_repr,l.change_message,c.app_label,c.model,l.user_id FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id ORDER BY l.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantMessages := []string{
		adminAdditionMessage,
		adminChangeMessage([]string{"Name", "Search", "Filter unread"}),
		"",
	}
	wantReprs := []string{"Work", "Updated", "Updated"}
	for i, wantFlag := range []int{1, 2, 3} {
		var flag int
		var objectID, repr, message, app, model string
		var actorID int64
		if !rows.Next() {
			t.Fatalf("missing log entry %d", i)
		}
		if err := rows.Scan(&flag, &objectID, &repr, &message, &app, &model, &actorID); err != nil {
			t.Fatal(err)
		}
		if flag != wantFlag || objectID != strconv.FormatInt(id, 10) || repr != wantReprs[i] || message != wantMessages[i] || app != "bookmarks" || model != "bookmarkbundle" || actorID != actor.ID {
			t.Errorf("log %d: flag=%d id=%q repr=%q message=%q type=%s.%s actor=%d", i, flag, objectID, repr, message, app, model, actorID)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected extra log entry")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
