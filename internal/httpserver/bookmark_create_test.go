package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkURLValidatorAllowsCredentialsLikeUpstream(t *testing.T) {
	if !validBookmarkURL("https://alice:secret@example.com/path") {
		t.Fatal("upstream URLValidator accepts URL userinfo")
	}
}

func TestBookmarkAPICreateFetchesMetadataWithoutOverwritingProvidedTitle(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Site title</title><meta name="description" content="Site description"></head></html>`))
	}))
	defer page.Close()
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "metadata", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "cccccccccccccccccccccccccccccccccccccccc"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	requestBody, _ := json.Marshal(map[string]any{"url": page.URL, "title": "User title"})
	req := httptest.NewRequest(http.MethodPost, "/api/bookmarks/", strings.NewReader(string(requestBody)))
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(db, cfg, t.TempDir()).ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var item struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Title != "User title" || item.Description != "Site description" {
		t.Fatalf("metadata merge: %+v", item)
	}
}

func TestBookmarkAPICheckReturnsExistingBookmarkMetadataAndAutoTags(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Check title</title><meta name="description" content="Check description"></head></html>`))
	}))
	defer page.Close()
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "check", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "dddddddddddddddddddddddddddddddddddddddd"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET auto_tagging_rules = ? WHERE user_id = ?`, "127.0.0.1 reviewed", user.ID); err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: page.URL, Title: "Saved title"})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("check %s: %d %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	missing := request("/api/bookmarks/check/")
	if missing["bookmark"] != nil || missing["auto_tags"] == nil {
		t.Fatalf("missing URL: %#v", missing)
	}
	metadata, ok := missing["metadata"].(map[string]any)
	if !ok || metadata["url"] != nil || metadata["title"] != nil {
		t.Fatalf("missing URL metadata: %#v", missing)
	}
	withURL := request("/api/bookmarks/check/?url=" + url.QueryEscape(page.URL))
	metadata, ok = withURL["metadata"].(map[string]any)
	if !ok || metadata["url"] != page.URL || metadata["title"] != "Check title" || metadata["description"] != "Check description" {
		t.Fatalf("metadata: %#v", withURL)
	}
	bookmark, ok := withURL["bookmark"].(map[string]any)
	if !ok || bookmark["id"] != float64(item.ID) || bookmark["title"] != "Saved title" {
		t.Fatalf("existing bookmark: %#v", withURL)
	}
	tags, ok := withURL["auto_tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "reviewed" {
		t.Fatalf("auto tags: %#v", withURL)
	}
}
