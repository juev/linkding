package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestManifestAndOpenSearchRespectContextPath(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	manifest := httptest.NewRecorder()
	handler.ServeHTTP(manifest, httptest.NewRequest(http.MethodGet, "/linkding/manifest.json", nil))
	if manifest.Code != http.StatusOK || manifest.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("manifest: status %d headers %v", manifest.Code, manifest.Header())
	}
	var body struct {
		Scope     string `json:"scope"`
		Shortcuts []struct {
			URL string `json:"url"`
		} `json:"shortcuts"`
		ShareTarget struct {
			Action string `json:"action"`
		} `json:"share_target"`
	}
	if err := json.Unmarshal(manifest.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Scope != "/linkding/" || len(body.Shortcuts) != 5 || body.Shortcuts[0].URL != "/linkding/bookmarks/new" || body.ShareTarget.Action != "/linkding/bookmarks/new" {
		t.Fatalf("manifest: %+v", body)
	}
	guest, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "guest_theme", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET theme = 'dark', custom_css = 'body {color: red}' WHERE user_id = ?`, guest.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_globalsettings (landing_page, guest_profile_user_id, enable_link_prefetch) VALUES ('login', ?, 0)`, guest.ID); err != nil {
		t.Fatal(err)
	}
	guestManifest := httptest.NewRecorder()
	handler.ServeHTTP(guestManifest, httptest.NewRequest(http.MethodGet, "/linkding/manifest.json", nil))
	var themed struct {
		BackgroundColor string `json:"background_color"`
	}
	if err := json.Unmarshal(guestManifest.Body.Bytes(), &themed); err != nil || themed.BackgroundColor != "#161822" {
		t.Fatalf("guest profile theme: %+v err=%v", themed, err)
	}
	customCSS := httptest.NewRecorder()
	handler.ServeHTTP(customCSS, httptest.NewRequest(http.MethodGet, "/linkding/custom_css", nil))
	if customCSS.Code != http.StatusOK || customCSS.Body.String() != "body {color: red}" ||
		customCSS.Header().Get("Content-Type") != "text/css" || customCSS.Header().Get("Cache-Control") != "public, max-age=2592000" {
		t.Fatalf("guest custom CSS: status %d headers %v body %q", customCSS.Code, customCSS.Header(), customCSS.Body.String())
	}
	opensearch := httptest.NewRecorder()
	handler.ServeHTTP(opensearch, httptest.NewRequest(http.MethodGet, "/linkding/opensearch.xml", nil))
	if opensearch.Code != http.StatusOK || opensearch.Header().Get("Content-Type") != "application/opensearchdescription+xml" ||
		!strings.Contains(opensearch.Body.String(), `http://example.com/linkding/bookmarks?client=opensearch&amp;q={searchTerms}`) ||
		!strings.Contains(opensearch.Body.String(), `http://example.com/linkding/static/favicon.ico`) {
		t.Fatalf("opensearch: status %d body %q", opensearch.Code, opensearch.Body.String())
	}
}
