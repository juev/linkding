package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminPermissionsDirectGroupAndSuperuser(t *testing.T) {
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
	staff, err := users.CreateUser(ctx, auth.NewUser{Username: "staff", Password: "password", IsStaff: true})
	if err != nil {
		t.Fatal(err)
	}
	superuser, err := users.CreateUser(ctx, auth.NewUser{Username: "super", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	grantTestUserPermission(t, db, staff.ID, "bookmarks", "tag", "view_tag")
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_group(id,name) VALUES (101,'Editors')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_user_groups(user_id,group_id) VALUES (?,101)`, staff.ID); err != nil {
		t.Fatal(err)
	}
	grantTestGroupPermission(t, db, 101, "bookmarks", "tag", "add_tag")
	got, err := loadAdminPermissions(ctx, db, "sqlite", staff, "bookmarks", "tag")
	if err != nil || !got.View || !got.Add || got.Change || got.Delete || !got.canList() {
		t.Fatalf("staff tag permissions: %+v %v", got, err)
	}
	got, err = loadAdminPermissions(ctx, db, "sqlite", staff, "bookmarks", "bookmark")
	if err != nil || got.any() {
		t.Fatalf("ungranted bookmark permissions: %+v %v", got, err)
	}
	got, err = loadAdminPermissions(ctx, db, "sqlite", superuser, "bookmarks", "bookmark")
	if err != nil || !got.View || !got.Add || !got.Change || !got.Delete {
		t.Fatalf("superuser permissions: %+v %v", got, err)
	}
	key, err := users.CreateSession(ctx, staff.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: key})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if page := get("/admin/"); page.Code != 200 || !strings.Contains(page.Body.String(), "Tags") || strings.Contains(page.Body.String(), "Bookmarks: Bookmarks") {
		t.Fatalf("staff dashboard: %d %q", page.Code, page.Body.String())
	}
	if page := get("/admin/bookmarks/tag/"); page.Code != 200 {
		t.Fatalf("permitted tag list: %d", page.Code)
	}
	if page := get("/admin/bookmarks/bookmark/"); page.Code != 403 {
		t.Fatalf("unpermitted bookmark list: %d", page.Code)
	}
}
