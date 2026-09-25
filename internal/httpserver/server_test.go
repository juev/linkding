package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestHealthAndContextPath(t *testing.T) {
	db, err := store.Open(context.Background(), config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "logo.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(db, config.Config{DBEngine: "sqlite", ContextPath: "linkding/"}, staticDir)
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/linkding/health", http.StatusOK},
		{"/health", http.StatusNotFound},
		{"/linkding/static/logo.svg", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != tc.want {
			t.Errorf("GET %s: got %d, want %d", tc.path, response.Code, tc.want)
		}
		if tc.path == "/linkding/health" {
			var body struct {
				Version string `json:"version"`
				Status  string `json:"status"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Version != UpstreamVersion || body.Status != "healthy" {
				t.Fatalf("health = %+v", body)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/linkding/health", nil))
	if response.Code != http.StatusInternalServerError || !containsUnhealthy(response.Body.String()) {
		t.Fatalf("closed database health: status %d, body %q", response.Code, response.Body.String())
	}
}

func containsUnhealthy(body string) bool {
	var decoded struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(body), &decoded) == nil && decoded.Status == "unhealthy"
}

func TestProfileAPIWithPinnedTokenAndDefaultProfile(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepository(db, "sqlite")
	user, err := repo.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "parity-password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'existing', '2026-09-25 10:00:00', ?)`, token, user.ID); err != nil {
		t.Fatal(err)
	}
	sessionKey, err := repo.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, config.Config{DBEngine: "sqlite", ContextPath: "linkding/"}, t.TempDir())
	path := "/linkding/api/user/profile/"
	for _, tc := range []struct {
		method string
		auth   string
		cookie string
		want   int
	}{
		{http.MethodGet, "", "", http.StatusUnauthorized},
		{http.MethodGet, "Token wrong", "", http.StatusUnauthorized},
		{http.MethodGet, "Token " + token, "", http.StatusOK},
		{http.MethodGet, "Bearer " + token, "", http.StatusOK},
		{http.MethodGet, "", sessionKey, http.StatusOK},
		{http.MethodPost, "Token " + token, "", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, path, nil)
		req.Header.Set("Authorization", tc.auth)
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tc.cookie})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("%s %q: status %d, body %s", tc.method, tc.auth, response.Code, response.Body.String())
		}
		if tc.want == http.StatusOK {
			var got map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != 12 || got["theme"] != "auto" || got["search_preferences"] == nil || got["version"] != "1.47.0" {
				t.Fatalf("profile differs from pinned API response: %#v", got)
			}
		}
	}
}
