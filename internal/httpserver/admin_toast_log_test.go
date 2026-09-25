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

func TestAdminToastLogsAddChangeDeleteAndSkipsInvalidPost(t *testing.T) {
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
	base := "/admin/bookmarks/toast/"
	request := func(path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		values.Set("csrfmiddlewaretoken", csrf)
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	ownerID := strconv.FormatInt(owner.ID, 10)
	if got := request(base+"add/", url.Values{"key": {"first"}, "message": {"Initial"}, "owner": {ownerID}}); got.Code != http.StatusFound {
		t.Fatalf("add toast: %d %s", got.Code, got.Body.String())
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key='first'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	change := url.Values{"key": {"second"}, "message": {"Changed"}, "acknowledged": {"on"}, "owner": {strconv.FormatInt(actor.ID, 10)}}
	if got := request(base+strconv.FormatInt(id, 10)+"/change/", change); got.Code != http.StatusFound {
		t.Fatalf("change toast: %d %s", got.Code, got.Body.String())
	}
	if got := request(base+strconv.FormatInt(id, 10)+"/delete/", url.Values{"post": {"yes"}}); got.Code != http.StatusFound {
		t.Fatalf("delete toast: %d %s", got.Code, got.Body.String())
	}
	if got := request(base+"add/", url.Values{"key": {""}, "message": {"Invalid"}, "owner": {ownerID}}); got.Code != http.StatusOK {
		t.Fatalf("invalid add toast: %d %s", got.Code, got.Body.String())
	}

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
	wantFlags := []int{1, 2, 3}
	wantMessages := []string{adminAdditionMessage, adminChangeMessage([]string{"Key", "Message", "Acknowledged", "Owner"}), ""}
	if len(logs) != len(wantFlags) {
		t.Fatalf("logs=%d, want %d", len(logs), len(wantFlags))
	}
	for i, entry := range logs {
		if entry.flag != wantFlags[i] || entry.objectID != strconv.FormatInt(id, 10) || entry.repr != "Toast object ("+strconv.FormatInt(id, 10)+")" || entry.message != wantMessages[i] || entry.app != "bookmarks" || entry.model != "toast" || entry.actorID != actor.ID {
			t.Fatalf("log %d = %+v", i, entry)
		}
	}
}
