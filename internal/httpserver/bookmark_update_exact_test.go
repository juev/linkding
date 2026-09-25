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
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkAPIUpdateChecksExactURLWhileFormUsesNormalizedURL(t *testing.T) {
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
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "exact", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	first, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/first"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/second"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "abababababababababababababababababababab"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	path := "/api/bookmarks/" + strconv.FormatInt(first.ID, 10) + "/"
	call := func(value string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"url":"`+value+`"}`))
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	variant := "HTTPS://EXAMPLE.COM/second/"
	if got := call(variant); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), variant) {
		t.Fatalf("normalized variant on API: %d %s", got.Code, got.Body.String())
	}
	if got := call(second.URL); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "A bookmark with this URL already exists.") {
		t.Fatalf("exact duplicate on API: %d %s", got.Code, got.Body.String())
	}
	saved, err := repo.GetByID(ctx, user.ID, first.ID)
	if err != nil || saved.URL != variant {
		t.Fatalf("saved API URL: %q, err=%v", saved.URL, err)
	}
	if _, err := repo.UpdateData(ctx, user.ID, first.ID, bookmarks.UpdateInput{URL: &second.URL}); err != bookmarks.ErrDuplicateURL {
		t.Fatalf("normalized form duplicate: %v", err)
	}
}
