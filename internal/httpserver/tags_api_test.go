package httpserver

import (
	"context"
	"encoding/json"
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

func TestTagsAPICreateCaseInsensitiveAndDeleteRelations(t *testing.T) {
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
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "tags-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "tags-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://example.com/tag", TagNames: []string{"Unicode"}})
	if err != nil {
		t.Fatal(err)
	}
	var originalID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM bookmarks_tag WHERE owner_id = ?", alice.ID).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		key   string
		owner int64
	}{{strings.Repeat("a", 40), alice.ID}, {strings.Repeat("b", 40), bob.ID}} {
		if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)", fixture.key, time.Now().UTC(), fixture.owner); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(db, cfg, t.TempDir())
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Token "+token)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/api/tags/"
	if got := call(http.MethodGet, base, "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", got.Code)
	}
	aliceToken := strings.Repeat("a", 40)
	bobToken := strings.Repeat("b", 40)
	duplicate := call(http.MethodPost, base, aliceToken, `{"name":"unicode"}`)
	if duplicate.Code != http.StatusCreated {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body.String())
	}
	var tag apiTag
	if err := json.Unmarshal(duplicate.Body.Bytes(), &tag); err != nil {
		t.Fatal(err)
	}
	if tag.ID != originalID || tag.Name != "Unicode" {
		t.Fatalf("case-insensitive reuse: %+v", tag)
	}
	created := call(http.MethodPost, base, aliceToken, `{"name":"  New  "}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"name":"New"`) {
		t.Fatalf("tag name was not trimmed: %s", created.Body.String())
	}
	listed := call(http.MethodGet, base, aliceToken, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"count":2`) {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	path := base + strconv.FormatInt(originalID, 10) + "/"
	if got := call(http.MethodGet, path, bobToken, ""); got.Code != http.StatusNotFound {
		t.Fatalf("other owner: %d", got.Code)
	}
	if got := call(http.MethodDelete, path, aliceToken, ""); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM bookmarks_bookmark_tags WHERE bookmark_id = ?", item.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("relations remain: %d err=%v", count, err)
	}
}
