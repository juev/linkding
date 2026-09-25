package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAPIMissingObjectsUsePinnedDRFDetails(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "missing", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bookmark, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/missing"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "ffffffffffffffffffffffffffffffffffffffff"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	for _, tc := range []struct {
		method, path string
		status       int
		detail       string
	}{
		{http.MethodGet, "/api/bookmarks/999999/assets/", 404, "Bookmark does not exist"},
		{http.MethodGet, "/api/bookmarks/999999/assets/upload/", 405, `Method "GET" not allowed.`},
		{http.MethodGet, "/api/bookmarks/" + strconv.FormatInt(bookmark.ID, 10) + "/assets/999999/", 404, "No BookmarkAsset matches the given query."},
		{http.MethodGet, "/api/tags/999999/", 404, "No Tag matches the given query."},
		{http.MethodDelete, "/api/tags/999999/", 404, "No Tag matches the given query."},
		{http.MethodGet, "/api/bundles/999999/", 404, "No BookmarkBundle matches the given query."},
		{http.MethodDelete, "/api/bundles/999999/", 404, "No BookmarkBundle matches the given query."},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var body struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: decode %q: %v", tc.method, tc.path, response.Body.String(), err)
		}
		if response.Code != tc.status || body.Detail != tc.detail {
			t.Errorf("%s %s: got %d %q, want %d %q", tc.method, tc.path, response.Code, body.Detail, tc.status, tc.detail)
		}
	}
}
