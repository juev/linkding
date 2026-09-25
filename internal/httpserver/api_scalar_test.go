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
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAPIScalarStringValidationMatchesPinnedDRF(t *testing.T) {
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
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "scalar", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "1212121212121212121212121212121212121212"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	call := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s body=%s: status %d, response %s", method, path, body, response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	fieldError := func(path, body, field, message string) {
		t.Helper()
		result := call(http.MethodPost, path, body, http.StatusBadRequest)
		values, ok := result[field].([]any)
		if !ok || len(values) != 1 || values[0] != message {
			t.Fatalf("%s %s: %v, want %s=%q", path, body, result, field, message)
		}
	}
	if got := call(http.MethodPost, "/api/tags/", `{"name":123}`, http.StatusCreated); got["name"] != "123" {
		t.Fatalf("numeric tag name: %v", got)
	}
	if got := call(http.MethodPost, "/api/tags/", `{"name":1e2}`, http.StatusCreated); got["name"] != "100.0" {
		t.Fatalf("floating tag name: %v", got)
	}
	fieldError("/api/tags/", `{"name":true}`, "name", "Not a valid string.")
	fieldError("/api/tags/", `{"name":null}`, "name", "This field may not be null.")
	if got := call(http.MethodPost, "/api/bundles/", `{"name":123,"search":42}`, http.StatusCreated); got["name"] != "123" || got["search"] != "42" {
		t.Fatalf("numeric bundle fields: %v", got)
	}
	fieldError("/api/bundles/", `{"name":true}`, "name", "Not a valid string.")
	fieldError("/api/bundles/", `{"name":null}`, "name", "This field may not be null.")
	fieldError("/api/bundles/", `{"name":"x","filter_unread":123}`, "filter_unread", `"123" is not a valid choice.`)
	fieldError("/api/bookmarks/", `{"url":123}`, "url", "Enter a valid URL.")
	fieldError("/api/bookmarks/", `{"url":true}`, "url", "Not a valid string.")
	fieldError("/api/bookmarks/", `{"url":null}`, "url", "This field may not be null.")
	fieldError("/api/bookmarks/", `[]`, "non_field_errors", "Invalid data. Expected a dictionary, but got list.")
	fieldError("/api/bookmarks/", `null`, "non_field_errors", "No data provided")
	fieldError("/api/bookmarks/", ``, "url", "This field is required.")
	for _, tc := range []struct {
		path, body, detail string
	}{
		{"/api/bookmarks/", `{`, `JSON parse error - Expecting property name enclosed in double quotes: line 1 column 2 (char 1)`},
		{"/api/tags/", `{"name":`, `JSON parse error - Expecting value: line 1 column 9 (char 8)`},
		{"/api/bundles/", `{"name":"x",}`, `JSON parse error - Illegal trailing comma before end of object: line 1 column 12 (char 11)`},
		{"/api/bookmarks/", `{} {}`, `JSON parse error - Extra data: line 1 column 4 (char 3)`},
	} {
		result := call(http.MethodPost, tc.path, tc.body, http.StatusBadRequest)
		if result["detail"] != tc.detail {
			t.Errorf("%s %s: detail=%v, want %q", tc.path, tc.body, result["detail"], tc.detail)
		}
	}
	created := call(http.MethodPost, "/api/bookmarks/?disable_scraping=1", `{"url":"https://example.com/scalar","title":123,"tag_names":[456]}`, http.StatusCreated)
	if created["title"] != "123" || len(created["tag_names"].([]any)) != 1 || created["tag_names"].([]any)[0] != "456" {
		t.Fatalf("numeric bookmark fields: %v", created)
	}
	fieldError("/api/bookmarks/", `{"url":"https://example.com/invalid-title","title":true}`, "title", "Not a valid string.")
	id := int64(created["id"].(float64))
	updated := call(http.MethodPatch, "/api/bookmarks/"+strconv.FormatInt(id, 10)+"/", `{"title":789}`, http.StatusOK)
	if updated["title"] != "789" {
		t.Fatalf("numeric PATCH title: %v", updated)
	}
}
