package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPageURLRemovesZeroOffsetLikeDRF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/bookmarks/?sort=title_asc&limit=2&offset=2", nil)
	if got := pageURL(req, 2, 0); got != "http://example.com/api/bookmarks/?limit=2&sort=title_asc" {
		t.Fatalf("previous page URL: %s", got)
	}
}

func TestBookmarkAPIReadUsesOwnerAndPinnedFields(t *testing.T) {
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
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), alice.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	stamp := time.Date(2026, 9, 25, 9, 35, 50, 724354000, time.UTC)
	item, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://example.com/a", Title: "A", TagNames: []string{"Go", "Test"}, DateAdded: &stamp, DateModified: &stamp})
	if err != nil {
		t.Fatal(err)
	}
	bobsItem, _, err := repo.CreateOrUpdateData(ctx, bob.ID, bookmarks.CreateInput{URL: "https://example.com/b"})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	path := "/linkding/api/bookmarks/"
	for _, tc := range []struct {
		path string
		auth string
		want int
	}{
		{path, "", http.StatusUnauthorized},
		{path, "Token " + token, http.StatusOK},
		{path + "?limit=1&offset=1", "Token " + token, http.StatusOK},
		{path + "99999/", "Token " + token, http.StatusNotFound},
		{path + "1/", "Token " + token, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", tc.auth)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("GET %s: got %d, want %d: %s", tc.path, response.Code, tc.want, response.Body.String())
		}
		if strings.HasSuffix(response.Body.String(), "\n") {
			t.Fatalf("GET %s: DRF response must not end in newline", tc.path)
		}
		if tc.path == path && tc.want == http.StatusOK {
			var body struct {
				Count   int              `json:"count"`
				Results []map[string]any `json:"results"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Count != 1 || len(body.Results) != 1 {
				t.Fatalf("list: %+v", body)
			}
			result := body.Results[0]
			if len(result) != 16 || result["id"] != float64(item.ID) || result["date_added"] != "2026-09-25T09:35:50.724354Z" ||
				result["web_archive_snapshot_url"] != "https://web.archive.org/web/20260925093550/https://example.com/a" {
				t.Fatalf("pinned bookmark fields: %#v", result)
			}
		}
	}
	for _, tc := range []struct {
		path  string
		count int
	}{
		{path + "?q=%23Go", 1},
		{path + "?q=%23Missing", 0},
		{path + "archived/?q=%23Go", 0},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var body struct {
			Count int `json:"count"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Count != tc.count {
			t.Fatalf("filtered GET %s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	archivePath := fmt.Sprintf("%s%d/archive/", path, item.ID)
	for _, tc := range []struct {
		path string
		want int
	}{
		{archivePath, http.StatusNoContent},
		{fmt.Sprintf("%s%d/archive/", path, bobsItem.ID), http.StatusNotFound},
		{fmt.Sprintf("%s%d/unarchive/", path, item.ID), http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("POST %s: got %d, want %d: %s", tc.path, response.Code, tc.want, response.Body.String())
		}
	}
	item, err = repo.GetByID(ctx, alice.ID, item.ID)
	if err != nil || item.IsArchived {
		t.Fatalf("archive round trip: %+v, %v", item, err)
	}
	session, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin string
		csrf   bool
		want   int
	}{
		{"", false, http.StatusForbidden},
		{"http://evil.example", true, http.StatusForbidden},
		{"http://example.com", true, http.StatusNoContent},
	} {
		req := httptest.NewRequest(http.MethodPost, archivePath, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: secret})
		if tc.csrf {
			req.Header.Set("X-CSRFToken", secret)
		}
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("session POST origin %q csrf=%t: %d %s", tc.origin, tc.csrf, response.Code, response.Body.String())
		}
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"url":"HTTPS://Example.COM/new/","title":"New","tag_names":["Go"]}`, http.StatusCreated},
		{`{"url":"https://example.com/new","title":"Merged","tag_names":["Test"]}`, http.StatusCreated},
		{`{}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, path+"?disable_scraping=1&disable_html_snapshot=1", strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("create %s: got %d, want %d: %s", tc.body, response.Code, tc.want, response.Body.String())
		}
		if tc.want == http.StatusCreated {
			if response.Header().Get("Location") == "" {
				t.Fatal("missing Location header")
			}
			var result map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["title"] == "" || result["url"] == "" {
				t.Fatalf("create response: %#v", result)
			}
		}
	}
	var createdCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmark WHERE owner_id = ?`, alice.ID).Scan(&createdCount); err != nil || createdCount != 2 {
		t.Fatalf("duplicate create count=%d err=%v", createdCount, err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPatch, fmt.Sprintf("%s%d/", path, item.ID), `{"title":"Patched"}`, http.StatusOK},
		{http.MethodPut, fmt.Sprintf("%s%d/", path, item.ID), `{}`, http.StatusBadRequest},
		{http.MethodPut, fmt.Sprintf("%s%d/", path, item.ID), `{"url":"https://example.com/a"}`, http.StatusOK},
		{http.MethodPatch, fmt.Sprintf("%s%d/", path, bobsItem.ID), `{"title":"No access"}`, http.StatusNotFound},
		{http.MethodPatch, fmt.Sprintf("%s%d/", path, item.ID), `{"url":"HTTPS://Example.COM/new/"}`, http.StatusBadRequest},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("%s %s: got %d, want %d: %s", tc.method, tc.path, response.Code, tc.want, response.Body.String())
		}
	}
	item, err = repo.GetByID(ctx, alice.ID, item.ID)
	if err != nil || item.Title != "Patched" || !slices.Equal(item.TagNames, []string{"Go", "Test"}) {
		t.Fatalf("update preserved fields: %+v, %v", item, err)
	}
	for _, directory := range []string{"previews", "assets"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	preview := filepath.Join(cfg.DataDir, "previews", "preview.png")
	asset := filepath.Join(cfg.DataDir, "assets", "snapshot.html")
	for _, file := range []string{preview, asset} {
		if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET preview_image_file = ? WHERE id = ?`, "preview.png", item.ID); err != nil {
		t.Fatal(err)
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, time.Now().UTC(), "snapshot.html", 4,
		"snapshot", "text/html", "Snapshot", "complete", false, item.ID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id = ? WHERE id = ?`, assetID, item.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   int64
		want int
	}{{bobsItem.ID, http.StatusNotFound}, {item.ID, http.StatusNoContent}} {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("%s%d/", path, tc.id), nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("DELETE %d: %d %s", tc.id, response.Code, response.Body.String())
		}
	}
	for _, file := range []string{preview, asset} {
		if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file remains after delete: %s: %v", file, err)
		}
	}
	for _, query := range []string{
		`SELECT count(*) FROM bookmarks_bookmark WHERE id = ?`,
		`SELECT count(*) FROM bookmarks_bookmark_tags WHERE bookmark_id = ?`,
		`SELECT count(*) FROM bookmarks_bookmarkasset WHERE bookmark_id = ?`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query, item.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("delete relation: %q count=%d err=%v", query, count, err)
		}
	}
}
