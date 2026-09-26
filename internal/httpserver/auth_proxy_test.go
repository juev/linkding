package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAuthProxyCreatesSwitchesAndRemovesSession(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), EnableAuthProxy: true, AuthProxyUsernameHeader: "Custom-User", DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	disabled := cfg
	disabled.EnableAuthProxy = false
	disabledRequest := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	disabledRequest.Header.Set("Custom-User", "alice")
	disabledResponse := httptest.NewRecorder()
	New(db, disabled, t.TempDir()).ServeHTTP(disabledResponse, disabledRequest)
	if disabledResponse.Code != 302 {
		t.Fatalf("disabled auth proxy accepted a header: %d", disabledResponse.Code)
	}
	call := func(username string, session *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
		if username != "" {
			r.Header.Set("Custom-User", username)
		}
		if session != nil {
			r.AddCookie(session)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := call("", nil); got.Code != 302 {
		t.Fatalf("anonymous: %d", got.Code)
	}
	first := call("alice", nil)
	if first.Code != 200 {
		t.Fatalf("first proxy request: %d %q", first.Code, first.Body.String())
	}
	var aliceSession *http.Cookie
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			aliceSession = cookie
		}
	}
	if aliceSession == nil || aliceSession.Value == "" {
		t.Fatal("proxy login did not set session")
	}
	var password string
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE username='alice'`).Scan(&password); err != nil || password != "!" {
		t.Fatalf("proxy-created account password: %q %v", password, err)
	}
	if got := call("alice", aliceSession); got.Code != 200 {
		t.Fatalf("same user session failed: %d", got.Code)
	} else {
		for _, cookie := range got.Result().Cookies() {
			if cookie.Name == auth.SessionCookieName {
				t.Fatal("same user session was recreated")
			}
		}
	}
	switched := call("bob", aliceSession)
	if switched.Code != 200 {
		t.Fatalf("switch user: %d", switched.Code)
	}
	var bobSession *http.Cookie
	for _, cookie := range switched.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			bobSession = cookie
		}
	}
	if bobSession == nil || bobSession.Value == aliceSession.Value {
		t.Fatal("switch did not rotate session")
	}
	if _, err := auth.NewRepository(db, "sqlite").AuthenticateSession(ctx, aliceSession.Value); err != auth.ErrInvalidCredentials {
		t.Fatalf("old session still valid: %v", err)
	}
	removed := call("", bobSession)
	if removed.Code != 302 {
		t.Fatalf("missing proxy header should log out: %d", removed.Code)
	}
	if _, err := auth.NewRepository(db, "sqlite").AuthenticateSession(ctx, bobSession.Value); err != auth.ErrInvalidCredentials {
		t.Fatalf("session survived header removal: %v", err)
	}
	if got := call("bob", nil); got.Code != 200 {
		t.Fatalf("existing proxy user: %d", got.Code)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET is_active=0 WHERE username='bob'`); err != nil {
		t.Fatal(err)
	}
	if got := call("bob", nil); got.Code != 302 {
		t.Fatalf("inactive proxy user: %d", got.Code)
	}
	metaConfig := cfg
	metaConfig.AuthProxyUsernameHeader = "HTTP_X_REMOTE_USER"
	metaRequest := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	metaRequest.Header.Set("X-Remote-User", "alice")
	metaResponse := httptest.NewRecorder()
	New(db, metaConfig, t.TempDir()).ServeHTTP(metaResponse, metaRequest)
	if metaResponse.Code != http.StatusOK {
		t.Fatalf("Django META header did not authenticate: %d", metaResponse.Code)
	}
	if len(metaResponse.Result().Cookies()) == 0 {
		t.Fatal("Django META header did not create a session")
	}
	defaultConfig := cfg
	defaultConfig.AuthProxyUsernameHeader = "REMOTE_USER"
	defaultRequest := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	defaultRequest.Header.Set("Remote-User", "alice")
	defaultResponse := httptest.NewRecorder()
	New(db, defaultConfig, t.TempDir()).ServeHTTP(defaultResponse, defaultRequest)
	if defaultResponse.Code != http.StatusOK {
		t.Fatalf("default proxy header did not authenticate: %d", defaultResponse.Code)
	}
}
