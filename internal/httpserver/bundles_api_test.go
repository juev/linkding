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

func TestBundlesAPICRUDOrderAndSearch(t *testing.T) {
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
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "bundles", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "cccccccccccccccccccccccccccccccccccccccc"
	if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)", token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/one", TagNames: []string{"Go"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/two", TagNames: []string{"Other"}}); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Token "+token)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/api/bundles/"
	firstResponse := call(http.MethodPost, base, `{"name":" Go ","any_tags":" Go "}`)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", firstResponse.Code, firstResponse.Body.String())
	}
	var first apiBundle
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Order != 0 || first.FilterUnread != "off" || first.AnyTags != "Go" || first.Name != "Go" {
		t.Fatalf("first defaults: %+v", first)
	}
	secondResponse := call(http.MethodPost, base, `{"name":"Other"}`)
	if secondResponse.Code != http.StatusCreated {
		t.Fatalf("second: %d %s", secondResponse.Code, secondResponse.Body.String())
	}
	var second apiBundle
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Order != 1 {
		t.Fatalf("second order: %+v", second)
	}
	list := call(http.MethodGet, base, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"count":2`) {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	filtered := call(http.MethodGet, "/api/bookmarks/?bundle="+strconv.FormatInt(first.ID, 10), "")
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), `"count":1`) || !strings.Contains(filtered.Body.String(), "example.com/one") {
		t.Fatalf("bundle search: %d %s", filtered.Code, filtered.Body.String())
	}
	path := base + strconv.FormatInt(first.ID, 10) + "/"
	patched := call(http.MethodPatch, path, `{"filter_unread":"yes","name":"Go items"}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"filter_unread":"yes"`) {
		t.Fatalf("patch: %d %s", patched.Code, patched.Body.String())
	}
	if got := call(http.MethodDelete, path, ""); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	remaining := call(http.MethodGet, base+strconv.FormatInt(second.ID, 10)+"/", "")
	if remaining.Code != http.StatusOK || !strings.Contains(remaining.Body.String(), `"order":0`) {
		t.Fatalf("renumber: %d %s", remaining.Code, remaining.Body.String())
	}
}
