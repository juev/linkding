package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkBundleAndAssetOptionsMetadata(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	owner, err := users.CreateUser(ctx, auth.NewUser{Username: "options-owner", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := users.CreateUser(ctx, auth.NewUser{Username: "options-other", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), owner.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	bookmark, _, err := repo.CreateOrUpdateData(ctx, owner.ID, bookmarks.CreateInput{URL: "https://example.com/options"})
	if err != nil {
		t.Fatal(err)
	}
	otherBookmark, _, err := repo.CreateOrUpdateData(ctx, other.ID, bookmarks.CreateInput{URL: "https://example.com/private-options"})
	if err != nil {
		t.Fatal(err)
	}
	name := "options bundle"
	bundle, err := createBundle(httptest.NewRequest(http.MethodPost, "/", nil), cfg, db, owner.ID, bundleInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	base := "/linkding/api/bookmarks/"
	bookmarkID := fmt.Sprint(bookmark.ID)
	bundleBase := "/linkding/api/bundles/"

	for _, tc := range []struct {
		path    string
		name    string
		allow   string
		actions bool
	}{
		{base, "Bookmark List", "GET, POST, HEAD, OPTIONS", true},
		{base + "archived/", "Archived", "GET, HEAD, OPTIONS", false},
		{base + "shared/", "Shared", "GET, HEAD, OPTIONS", false},
		{base + "check/", "Check", "GET, HEAD, OPTIONS", false},
		{base + "singlefile/", "Singlefile", "POST, OPTIONS", true},
		{base + bookmarkID + "/", "Bookmark Instance", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS", true},
		{base + "999999/", "Bookmark Instance", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS", false},
		{base + fmt.Sprint(otherBookmark.ID) + "/", "Bookmark Instance", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS", false},
		{base + bookmarkID + "/archive/", "Archive", "POST, OPTIONS", true},
		{base + bookmarkID + "/unarchive/", "Unarchive", "POST, OPTIONS", true},
		{base + "999999/archive/", "Archive", "POST, OPTIONS", true},
		{base + "999999/unarchive/", "Unarchive", "POST, OPTIONS", true},
		{base + bookmarkID + "/assets/", "Bookmark Asset List", "GET, HEAD, OPTIONS", false},
		{base + bookmarkID + "/assets/upload/", "Upload", "POST, OPTIONS", true},
		{base + "999999/assets/upload/", "Upload", "POST, OPTIONS", true},
		{base + bookmarkID + "/assets/999999/", "Bookmark Asset Instance", "GET, DELETE, HEAD, OPTIONS", false},
		{base + bookmarkID + "/assets/999999/download/", "Download", "GET, HEAD, OPTIONS", false},
		{bundleBase, "Bookmark Bundle List", "GET, POST, HEAD, OPTIONS", true},
		{bundleBase + fmt.Sprint(bundle.ID) + "/", "Bookmark Bundle Instance", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS", true},
		{bundleBase + "999999/", "Bookmark Bundle Instance", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, tc.path, nil)
			request.Header.Set("Authorization", "Token "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Allow") != tc.allow || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("OPTIONS %s: status=%d allow=%q content-type=%q body=%s", tc.path, response.Code, response.Header().Get("Allow"), response.Header().Get("Content-Type"), response.Body.String())
			}
			var body struct {
				Name        string                     `json:"name"`
				Description string                     `json:"description"`
				Renders     []string                   `json:"renders"`
				Parses      []string                   `json:"parses"`
				Actions     map[string]json.RawMessage `json:"actions"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Name != tc.name || body.Description != "" || strings.Join(body.Renders, ",") != "application/json,text/html" || strings.Join(body.Parses, ",") != "application/json,application/x-www-form-urlencoded,multipart/form-data" {
				t.Fatalf("OPTIONS %s metadata: %+v", tc.path, body)
			}
			var document map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			_, hasActions := document["actions"]
			if hasActions != tc.actions {
				t.Fatalf("OPTIONS %s actions presence: got %t, want %t", tc.path, hasActions, tc.actions)
			}
			if tc.actions {
				method := http.MethodPost
				if tc.name == "Bookmark Instance" || tc.name == "Bookmark Bundle Instance" {
					method = http.MethodPut
				}
				if len(body.Actions) != 1 || len(body.Actions[method]) == 0 {
					t.Fatalf("OPTIONS %s action method: %#v, want %s", tc.path, body.Actions, method)
				}
			}
		})
	}
	// These bodies were captured from the pinned Python v1.47.0 runtime.
	file, err := os.Open("testdata/api_options_v1470.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var golden struct {
			Path string `json:"path"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &golden); err != nil {
			t.Fatal(err)
		}
		path := strings.Replace(golden.Path, "/bookmarks/1/assets/", "/bookmarks/"+bookmarkID+"/assets/", 1)
		request := httptest.NewRequest(http.MethodOptions, "/linkding"+path, nil)
		request.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != golden.Body {
			t.Errorf("pinned OPTIONS %s: status=%d\n got: %s\nwant: %s", path, response.Code, response.Body.String(), golden.Body)
		}
		if response.Header().Get("Content-Length") != fmt.Sprint(len(golden.Body)) {
			t.Errorf("pinned OPTIONS %s: content length %q, want %d", path, response.Header().Get("Content-Length"), len(golden.Body))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	anonymous := httptest.NewRequest(http.MethodOptions, base+"shared/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, anonymous)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Token" || response.Body.String() != `{"detail":"Authentication credentials were not provided."}` {
		t.Fatalf("anonymous shared OPTIONS: %d %q %q", response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
	}
}
