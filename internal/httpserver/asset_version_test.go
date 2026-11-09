package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

var assetURLPattern = regexp.MustCompile(`static/(bundle\.js|theme-light\.css|theme-dark\.css|icons\.svg)\?v=([^"#&]*)`)

// TestNoHardCodedAssetVersions fails when a template or Go string brings back
// a literal cache-busting version such as ?v=1.47.0.
func TestNoHardCodedAssetVersions(t *testing.T) {
	literal := regexp.MustCompile(`\?v=[0-9]`)
	files, err := filepath.Glob("*")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		source := strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".html")
		if !source || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(data), "\n") {
			if literal.MatchString(line) {
				t.Errorf("%s:%d has a hard-coded asset version; use ?v={{assetVersion}}", name, i+1)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked only %d files; the test runs in the wrong directory", checked)
	}
}

func writeStaticAssets(t *testing.T, dir, bundle string) {
	t.Helper()
	for name, content := range map[string]string{"bundle.js": bundle, "theme-light.css": "body{}", "theme-dark.css": "body{}", "icons.svg": "<svg/>", "logo.png": "png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// pageAssetVersions returns the distinct versions of the asset URLs on a page.
func pageAssetVersions(t *testing.T, body string) map[string]bool {
	t.Helper()
	matches := assetURLPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("page has no versioned asset URLs: %s", body)
	}
	versions := map[string]bool{}
	for _, match := range matches {
		versions[match[2]] = true
	}
	return versions
}

func renderLoginAssetVersion(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login/", nil))
	versions := pageAssetVersions(t, response.Body.String())
	if len(versions) != 1 {
		t.Fatalf("login page uses several asset versions: %v", versions)
	}
	for version := range versions {
		if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(version) {
			t.Fatalf("asset version %q is not a content hash", version)
		}
		return version
	}
	return ""
}

func TestAssetVersionFollowsFrontendContent(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), SessionCookieAge: 3600}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}

	oldBuild, newBuild := t.TempDir(), t.TempDir()
	writeStaticAssets(t, oldBuild, "console.log('v1')")
	writeStaticAssets(t, newBuild, "console.log('v2')")
	oldVersion := renderLoginAssetVersion(t, New(db, cfg, oldBuild))
	newVersion := renderLoginAssetVersion(t, New(db, cfg, newBuild))
	if oldVersion == newVersion {
		t.Fatalf("builds with different bundles share asset version %q", oldVersion)
	}
	handler := New(db, cfg, oldBuild)
	if again := renderLoginAssetVersion(t, handler); again != oldVersion {
		t.Fatalf("same content changed the version: %q then %q", oldVersion, again)
	}

	// A rebuild in place, as `npm run dev` does, changes the version without a restart.
	if err := os.WriteFile(filepath.Join(oldBuild, "bundle.js"), []byte("console.log('v3')"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(oldBuild, "bundle.js"), later, later); err != nil {
		t.Fatal(err)
	}
	rebuilt := renderLoginAssetVersion(t, handler)
	if rebuilt == oldVersion || rebuilt == newVersion {
		t.Fatalf("rebuilt bundle kept a previous version: %q", rebuilt)
	}

	// Pages behind a session use the same single version for every asset.
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "secret-password"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bookmarks", "/settings/general", "/settings/integrations", "/tags", "/bundles", "/bookmarks/new", "/change-password/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: key})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, response.Code)
		}
		versions := pageAssetVersions(t, response.Body.String())
		if len(versions) != 1 || !versions[rebuilt] {
			t.Fatalf("%s asset versions = %v, want only %q", path, versions, rebuilt)
		}
	}
}

func TestStaticCacheControlFollowsVersion(t *testing.T) {
	staticDir, dataDir := t.TempDir(), t.TempDir()
	writeStaticAssets(t, staticDir, "console.log('cache')")
	if err := os.MkdirAll(filepath.Join(dataDir, "favicons"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "favicons", "example.png"), []byte("icon"), 0o600); err != nil {
		t.Fatal(err)
	}
	assets := newAssetVersioner(staticDir)
	current := assets.Version()
	cases := []struct {
		path, want string
	}{
		{"/static/bundle.js?v=" + current, staticCacheForever},
		{"/static/theme-dark.css?v=" + current, staticCacheForever},
		{"/static/icons.svg?v=" + current, staticCacheForever},
		{"/static/bundle.js?v=1.47.0", "no-cache"},
		{"/static/bundle.js?v=000000000000", "no-cache"},
		{"/static/bundle.js", "no-cache"},
		{"/static/logo.png?v=" + current, "no-cache"},
		{"/static/example.png", "no-cache"},
	}
	for _, tc := range cases {
		response := httptest.NewRecorder()
		serveStaticFile(response, httptest.NewRequest(http.MethodGet, tc.path, nil), "/static/", staticDir, dataDir, assets)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != tc.want {
			t.Errorf("%s: status=%d Cache-Control=%q, want %q", tc.path, response.Code, response.Header().Get("Cache-Control"), tc.want)
		}
	}
}
