package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminUserHistory(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), TimeZone: "UTC"}
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
	viewer, err := users.CreateUser(ctx, auth.NewUser{Username: "viewer", Password: "password", IsStaff: true})
	if err != nil {
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
	handler := New(db, cfg, t.TempDir())
	request := func(method, path, session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/admin/auth/user/" + strconv.FormatInt(target.ID, 10) + "/history/"
	if got := request(http.MethodGet, path, adminSession); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Change history: target") || !strings.Contains(got.Body.String(), "doesn’t have a change history") {
		t.Fatalf("empty history: %d %s", got.Code, got.Body.String())
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAdminLog(ctx, tx, cfg.DBEngine, admin.ID, "auth", "user", strconv.FormatInt(target.ID, 10), target.Username, 1, adminAdditionMessage); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := request(http.MethodGet, path, adminSession); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Added.") || !strings.Contains(got.Body.String(), "1 entry") {
		t.Fatalf("logged history: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, path, viewerSession); got.Code != http.StatusForbidden {
		t.Fatalf("viewer without permission: %d", got.Code)
	}
	if got := request(http.MethodPost, path, adminSession); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("history accepted POST: %d", got.Code)
	}
	if got := request(http.MethodGet, "/admin/auth/user/999999/history/", adminSession); got.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d", got.Code)
	}
}

func TestAdminHistoryDate(t *testing.T) {
	cases := []struct {
		hour, minute int
		want         string
	}{{0, 0, "midnight"}, {12, 0, "noon"}, {9, 5, "9:05 a.m."}, {15, 0, "3 p.m."}}
	for _, test := range cases {
		value := time.Date(2026, time.September, 25, test.hour, test.minute, 0, 0, time.UTC)
		if got := adminHistoryDate(value); !strings.Contains(got, test.want) || !strings.HasPrefix(got, "Sept. 25, 2026, ") {
			t.Errorf("format %02d:%02d: %q", test.hour, test.minute, got)
		}
	}
}
