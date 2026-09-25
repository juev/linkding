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

func TestAdminTagLogsAddChangeDelete(t *testing.T) {
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
	session, err := users.CreateSession(ctx, actor.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(path string, values url.Values) {
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
	}
	base := "/admin/bookmarks/tag/"
	form := url.Values{"name": {"First"}, "date_added_0": {"2020-02-03"}, "date_added_1": {"04:05:06"}, "owner": {strconv.FormatInt(actor.ID, 10)}}
	request(base+"add/", form)
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_tag WHERE name='First'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	form.Set("name", "Second")
	request(base+strconv.FormatInt(id, 10)+"/change/", form)
	request(base+strconv.FormatInt(id, 10)+"/delete/", url.Values{"post": {"yes"}})

	rows, err := db.QueryContext(ctx, `SELECT l.action_flag,l.object_id,l.object_repr,l.change_message,c.app_label,c.model,l.user_id FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id ORDER BY l.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var logs []struct {
		flag                    int
		objectID, repr, message string
		app, model              string
		actorID                 int64
	}
	for rows.Next() {
		var item struct {
			flag                    int
			objectID, repr, message string
			app, model              string
			actorID                 int64
		}
		if err := rows.Scan(&item.flag, &item.objectID, &item.repr, &item.message, &item.app, &item.model, &item.actorID); err != nil {
			t.Fatal(err)
		}
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("logs=%d, want 3", len(logs))
	}
	wantFlags := []int{1, 2, 3}
	wantReprs := []string{"First", "Second", "Second"}
	wantMessages := []string{adminAdditionMessage, adminChangeMessage([]string{"Name"}), ""}
	for i, entry := range logs {
		if entry.flag != wantFlags[i] || entry.objectID != strconv.FormatInt(id, 10) || entry.repr != wantReprs[i] || entry.message != wantMessages[i] || entry.app != "bookmarks" || entry.model != "tag" || entry.actorID != actor.ID {
			t.Fatalf("log %d = %+v", i, entry)
		}
	}
}
