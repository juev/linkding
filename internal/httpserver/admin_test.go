package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/store"
)

func TestAdminTasksRequireStaffAndPaginate(t *testing.T) {
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
	staff, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	normal, err := users.CreateUser(ctx, auth.NewUser{Username: "normal", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	staffKey, err := users.CreateSession(ctx, staff.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normalKey, err := users.CreateSession(ctx, normal.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	queue := jobs.New(db, "sqlite")
	for i := range 101 {
		_, err := queue.Enqueue(ctx, "load_favicon", json.RawMessage(`{"bookmark_id":1}`))
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	handler := New(db, cfg, t.TempDir())
	get := func(path, session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if session != "" {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := get("/admin/tasks/", ""); got.Code != 302 || !strings.Contains(got.Header().Get("Location"), "/login/") {
		t.Fatalf("anonymous tasks: %d %q", got.Code, got.Header().Get("Location"))
	}
	if got := get("/admin/tasks/", normalKey); got.Code != 403 {
		t.Fatalf("non-staff tasks: %d", got.Code)
	}
	if got := get("/admin/", staffKey); got.Code != 200 || !strings.Contains(got.Body.String(), "Queued tasks") {
		t.Fatalf("dashboard: %d", got.Code)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (?,?,?)`, "admin-tag", time.Now().UTC(), staff.ID); err != nil {
		t.Fatal(err)
	}
	for _, definition := range adminModels {
		path := "/admin/" + definition.App + "/" + definition.Model + "/"
		if got := get(path, normalKey); got.Code != 403 {
			t.Fatalf("non-staff model %s: %d", path, got.Code)
		}
		got := get(path, staffKey)
		if got.Code != 200 || !strings.Contains(got.Body.String(), "Select "+strings.ToLower(definition.Label)+" to change") || !strings.Contains(got.Body.String(), "Pagination "+strings.ToLower(definition.Plural)) {
			t.Fatalf("model list %s: %d", path, got.Code)
		}
	}
	if got := get("/admin/bookmarks/tag/", staffKey); !strings.Contains(got.Body.String(), "admin-tag") {
		t.Fatalf("tag missing from admin list: %d", got.Code)
	}
	first := get("/admin/tasks/", staffKey)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "101 tasks") || !strings.Contains(first.Body.String(), "?p=2") || strings.Count(first.Body.String(), "load_favicon") != 100 {
		t.Fatalf("first tasks page: %d", first.Code)
	}
	second := get("/admin/tasks/?p=2", staffKey)
	if second.Code != 200 || strings.Count(second.Body.String(), "load_favicon") != 1 {
		t.Fatalf("second tasks page: %d", second.Code)
	}
}
