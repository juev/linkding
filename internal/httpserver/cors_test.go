package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestCORSMatchesPinnedAPIMiddleware(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/", CORSAllowedOrigins: "https://frontend.example.com,invalid, https://other.example.com/", DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	call := func(method, path, origin, requested string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if requested != "" {
			r.Header.Set("Access-Control-Request-Method", requested)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	preflight := call(http.MethodOptions, "/linkding/api/bookmarks/", "https://frontend.example.com", "POST")
	if preflight.Code != 200 || preflight.Body.Len() != 0 || preflight.Header().Get("Access-Control-Allow-Origin") != "https://frontend.example.com" || preflight.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, PATCH, DELETE, OPTIONS" || preflight.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" || preflight.Header().Get("Access-Control-Max-Age") != "86400" || preflight.Header().Get("Content-Length") != "0" {
		t.Fatalf("preflight: %d %v", preflight.Code, preflight.Header())
	}
	unknown := call(http.MethodOptions, "/linkding/api/bookmarks/", "https://unknown.example.com", "POST")
	if unknown.Code != 200 || unknown.Header().Get("Access-Control-Allow-Origin") != "" || !strings.Contains(unknown.Header().Get("Vary"), "Origin") {
		t.Fatalf("unknown origin: %d %v", unknown.Code, unknown.Header())
	}
	plain := call(http.MethodOptions, "/linkding/api/bookmarks/", "https://frontend.example.com", "")
	if plain.Code != 401 || plain.Header().Get("Access-Control-Allow-Origin") != "https://frontend.example.com" {
		t.Fatalf("plain options: %d %v", plain.Code, plain.Header())
	}
	unauthorized := call(http.MethodGet, "/linkding/api/bookmarks/", "https://frontend.example.com", "")
	if unauthorized.Code != 401 || unauthorized.Header().Get("Access-Control-Allow-Origin") != "https://frontend.example.com" || strings.Contains(unauthorized.Header().Get("Access-Control-Allow-Credentials"), "true") {
		t.Fatalf("API error: %d %v", unauthorized.Code, unauthorized.Header())
	}
	outside := call(http.MethodOptions, "/api/bookmarks/", "https://frontend.example.com", "POST")
	if outside.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(outside.Header().Get("Vary"), "Origin") {
		t.Fatalf("outside context path: %d %v", outside.Code, outside.Header())
	}
	login := call(http.MethodGet, "/linkding/login/", "https://frontend.example.com", "")
	if login.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(login.Header().Get("Vary"), "Origin") {
		t.Fatalf("non API path: %d %v", login.Code, login.Header())
	}
	for _, origin := range []string{"https://frontend.example.com/path", "https://user@frontend.example.com", "null"} {
		got := call(http.MethodOptions, "/linkding/api/bookmarks/", origin, "POST")
		if got.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("invalid origin %q accepted", origin)
		}
	}
}
