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

func TestAdminTokenLogsCRUD(t *testing.T) {
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
	actor, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
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
	session, err := users.CreateSession(ctx, actor.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		values.Set("csrfmiddlewaretoken", csrf)
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
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
	apiBase := "/admin/bookmarks/apitoken/"
	request(apiBase+"add/", url.Values{"name": {"Alice API"}, "user": {strconv.FormatInt(alice.ID, 10)}})
	var apiID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_apitoken WHERE name='Alice API'`).Scan(&apiID); err != nil {
		t.Fatal(err)
	}
	apiChange := apiBase + strconv.FormatInt(apiID, 10) + "/change/"
	request(apiChange, url.Values{"name": {"Bob API"}, "user": {strconv.FormatInt(bob.ID, 10)}})
	request(apiBase+strconv.FormatInt(apiID, 10)+"/delete/", url.Values{"post": {"yes"}})

	feedBase := "/admin/bookmarks/feedtoken/"
	request(feedBase+"add/", url.Values{"key": {"first-key"}, "user": {strconv.FormatInt(alice.ID, 10)}})
	request(feedBase+"first-key/change/", url.Values{"key": {"second-key"}, "user": {strconv.FormatInt(bob.ID, 10)}})
	request(feedBase+"second-key/delete/", url.Values{"post": {"yes"}})
	var oldKeyCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookmarks_feedtoken WHERE key='first-key'`).Scan(&oldKeyCount); err != nil || oldKeyCount != 1 {
		t.Fatalf("Django key-change behavior did not preserve original token: count=%d err=%v", oldKeyCount, err)
	}

	rows, err := db.QueryContext(ctx, `SELECT l.action_flag,l.object_id,l.object_repr,l.change_message,c.app_label,c.model,l.user_id FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id ORDER BY l.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type logEntry struct {
		flag                    int
		objectID, repr, message string
		app, model              string
		actorID                 int64
	}
	var got []logEntry
	for rows.Next() {
		var item logEntry
		if err := rows.Scan(&item.flag, &item.objectID, &item.repr, &item.message, &item.app, &item.model, &item.actorID); err != nil {
			t.Fatal(err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []logEntry{
		{1, strconv.FormatInt(apiID, 10), "Alice API (alice)", adminAdditionMessage, "bookmarks", "apitoken", actor.ID},
		{2, strconv.FormatInt(apiID, 10), "Bob API (bob)", adminChangeMessage([]string{"Name", "User"}), "bookmarks", "apitoken", actor.ID},
		{3, strconv.FormatInt(apiID, 10), "Bob API (bob)", "", "bookmarks", "apitoken", actor.ID},
		{1, "first-key", "first-key", adminAdditionMessage, "bookmarks", "feedtoken", actor.ID},
		{2, "second-key", "second-key", adminChangeMessage([]string{"Key", "User"}), "bookmarks", "feedtoken", actor.ID},
		{3, "second-key", "second-key", "", "bookmarks", "feedtoken", actor.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("logs=%+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("log %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
