package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
	"github.com/juev/linkding/internal/store"
)

func TestRootRespectsGuestLandingAndQuery(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.NewRepository(db, "sqlite").CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"landing_page": {"shared_bookmarks"}}
	if err := settings.UpdateGlobal(ctx, db, "sqlite", true, form); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	guest := httptest.NewRecorder()
	handler.ServeHTTP(guest, httptest.NewRequest(http.MethodGet, "/linkding/?q=test", nil))
	if guest.Code != http.StatusFound || guest.Header().Get("Location") != "/linkding/bookmarks/shared?q=test" {
		t.Fatalf("guest redirect: %d %q", guest.Code, guest.Header().Get("Location"))
	}
	ownerReq := httptest.NewRequest(http.MethodGet, "/linkding/?q=test", nil)
	ownerReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	owner := httptest.NewRecorder()
	handler.ServeHTTP(owner, ownerReq)
	if owner.Code != http.StatusFound || owner.Header().Get("Location") != "/linkding/bookmarks?q=test" {
		t.Fatalf("owner redirect: %d %q", owner.Code, owner.Header().Get("Location"))
	}
}
