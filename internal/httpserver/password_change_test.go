package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPasswordChangeKeepsCurrentSession(t *testing.T) {
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
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "initial_password"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	post := func(old, new1, new2 string) *httptest.ResponseRecorder {
		form := url.Values{"csrfmiddlewaretoken": {csrf}, "old_password": {old}, "new_password1": {new1}, "new_password2": {new2}}
		r := httptest.NewRequest(http.MethodPost, "/change-password/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: current})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := post("wrong", "Fresh Passphrase 2026!", "Fresh Passphrase 2026!"); got.Code != 422 || !strings.Contains(got.Body.String(), "old password was entered incorrectly") {
		t.Fatalf("wrong old password: %d %q", got.Code, got.Body.String())
	}
	if got := post("initial_password", "Fresh Passphrase 2026!", "other"); got.Code != 422 {
		t.Fatalf("mismatched new password: %d", got.Code)
	}
	if got := post("initial_password", "password", "password"); got.Code != 422 || !strings.Contains(got.Body.String(), "too short") && !strings.Contains(got.Body.String(), "too common") {
		t.Fatalf("weak new password: %d %q", got.Code, got.Body.String())
	}
	changed := post("initial_password", "Fresh Passphrase 2026!", "Fresh Passphrase 2026!")
	if changed.Code != 302 || changed.Header().Get("Location") != "/password-change-done/" {
		t.Fatalf("change: %d %q", changed.Code, changed.Header().Get("Location"))
	}
	if _, err := users.AuthenticatePassword(ctx, "alice", "Fresh Passphrase 2026!"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if _, err := users.AuthenticatePassword(ctx, "alice", "initial_password"); err != auth.ErrInvalidCredentials {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err := users.AuthenticateSession(ctx, current); err != nil {
		t.Fatalf("current session invalidated: %v", err)
	}
	if _, err := users.AuthenticateSession(ctx, other); err != auth.ErrInvalidCredentials {
		t.Fatalf("other session retained: %v", err)
	}
	doneReq := httptest.NewRequest(http.MethodGet, "/password-change-done/", nil)
	doneReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: current})
	done := httptest.NewRecorder()
	handler.ServeHTTP(done, doneReq)
	if done.Code != 200 || !strings.Contains(done.Body.String(), "Your password was changed successfully.") {
		t.Fatalf("done: %d %q", done.Code, done.Body.String())
	}
}
