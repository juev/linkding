package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestSharedBookmarkAPIEnforcesOwnerAndPublicSharing(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "share-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "share-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	carol, err := users.CreateUser(ctx, auth.NewUser{Username: "share-carol", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_sharing = 1 WHERE user_id IN (?, ?)", alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_public_sharing = 1 WHERE user_id IN (?, ?)", bob.ID, carol.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	fixtures := []struct {
		owner int64
		input bookmarks.CreateInput
	}{
		{alice.ID, bookmarks.CreateInput{URL: "https://example.com/alice", Title: "Alice shared", Shared: true}},
		{bob.ID, bookmarks.CreateInput{URL: "https://example.com/bob", Title: "Bob public", Shared: true, TagNames: []string{"special"}}},
		{bob.ID, bookmarks.CreateInput{URL: "https://example.com/archived", Title: "Bob archived", Shared: true, IsArchived: true}},
		{bob.ID, bookmarks.CreateInput{URL: "https://example.com/private", Title: "Bob private"}},
		{carol.ID, bookmarks.CreateInput{URL: "https://example.com/carol", Title: "Carol disabled", Shared: true}},
	}
	for _, fixture := range fixtures {
		if _, _, err := repo.CreateOrUpdateData(ctx, fixture.owner, fixture.input); err != nil {
			t.Fatal(err)
		}
	}
	const token = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)", token, time.Now().UTC(), alice.ID); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	check := func(path, authHeader string, wantStatus, wantCount int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != wantStatus {
			t.Fatalf("%s auth=%q: %d %s", path, authHeader, response.Code, response.Body.String())
		}
		if wantStatus == http.StatusOK {
			var body struct {
				Count   int `json:"count"`
				Results []struct {
					Title string `json:"title"`
				} `json:"results"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Count != wantCount || len(body.Results) != wantCount {
				t.Fatalf("%s auth=%q: %+v", path, authHeader, body)
			}
		}
	}
	base := "/api/bookmarks/shared/"
	check(base, "", http.StatusOK, 2)
	check(base, "Token "+token, http.StatusOK, 3)
	check(base+"?user=share-alice", "", http.StatusOK, 0)
	check(base+"?user=share-alice", "Token "+token, http.StatusOK, 1)
	check(base+"?user=missing", "", http.StatusOK, 2)
	check(base, "Token invalid", http.StatusUnauthorized, 0)
	post := httptest.NewRequest(http.MethodPost, base, nil)
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, post)
	var postBody struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(postResponse.Body.Bytes(), &postBody); err != nil {
		t.Fatal(err)
	}
	if postResponse.Code != http.StatusUnauthorized || postBody.Detail != "Authentication credentials were not provided." {
		t.Fatalf("anonymous POST shared: %d %s", postResponse.Code, postResponse.Body.String())
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET tag_search = 'lax' WHERE user_id = ?", bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_globalsettings (landing_page, guest_profile_user_id, enable_link_prefetch) VALUES ('login', ?, 0)", bob.ID); err != nil {
		t.Fatal(err)
	}
	check(base+"?q=special", "", http.StatusOK, 1)
}
