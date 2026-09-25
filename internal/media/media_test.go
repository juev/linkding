package media

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpserver"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/store"
)

func TestFaviconAndPreviewJobsPersistAndServeOriginalStaticPaths(t *testing.T) {
	const iconData = "icon-data"
	const imageData = "image-data"
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favicon":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(iconData))
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprint(len(imageData)))
			_, _ = w.Write([]byte(imageData))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Page</title><meta property="og:image" content="/image"></head></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer page.Close()
	ctx := context.Background()
	cfg := config.Config{
		DBEngine: "sqlite", DataDir: t.TempDir(), AllowedInternalHosts: "127.0.0.1",
		FaviconProvider: page.URL + "/favicon?url={url}", PreviewMaxSize: 1024,
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "media", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: page.URL + "/page"})
	if err != nil {
		t.Fatal(err)
	}
	processor := New(db, "sqlite", cfg)
	job := jobs.Job{Payload: []byte(fmt.Sprintf(`{"bookmark_id":%d}`, item.ID))}
	if err := processor.LoadFavicon(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := processor.LoadPreviewImage(ctx, job); err != nil {
		t.Fatal(err)
	}
	var favicon, preview string
	if err := db.QueryRowContext(ctx, "SELECT favicon_file, preview_image_file FROM bookmarks_bookmark WHERE id = ?", item.ID).Scan(&favicon, &preview); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ directory, filename, data string }{
		{"favicons", favicon, iconData}, {"previews", preview, imageData},
	} {
		if fixture.filename == "" {
			t.Fatalf("%s filename missing", fixture.directory)
		}
		content, err := os.ReadFile(filepath.Join(cfg.DataDir, fixture.directory, fixture.filename))
		if err != nil || !bytes.Equal(content, []byte(fixture.data)) {
			t.Fatalf("%s file: %q %v", fixture.directory, content, err)
		}
		req := httptest.NewRequest(http.MethodGet, "/static/"+fixture.filename, nil)
		response := httptest.NewRecorder()
		httpserver.New(db, cfg, t.TempDir()).ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.String() != fixture.data || response.Header().Get("Content-Security-Policy") != "sandbox" {
			t.Fatalf("%s static response: %d %q %q", fixture.directory, response.Code, response.Body.String(), response.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestWebArchiveJobSkipsSavedSnapshotUnlessForced(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Location", "/web/20260925000000/https://example.com/page")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
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
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "wayback", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://example.com/page"})
	if err != nil {
		t.Fatal(err)
	}
	processor := New(db, "sqlite", cfg)
	processor.waybackBase = server.URL
	job := jobs.Job{Payload: []byte(fmt.Sprintf(`{"bookmark_id":%d}`, item.ID))}
	if err := processor.SaveWebArchive(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := processor.SaveWebArchive(ctx, job); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("archive requests: %d", requests)
	}
	var savedURL string
	if err := db.QueryRowContext(ctx, "SELECT web_archive_snapshot_url FROM bookmarks_bookmark WHERE id = ?", item.ID).Scan(&savedURL); err != nil {
		t.Fatal(err)
	}
	if savedURL != "https://web.archive.org/web/20260925000000/https://example.com/page" {
		t.Fatalf("archive URL: %q", savedURL)
	}
	forced := jobs.Job{Payload: []byte(fmt.Sprintf(`{"bookmark_id":%d,"force_update":true}`, item.ID))}
	if err := processor.SaveWebArchive(ctx, forced); err != nil || requests != 2 {
		t.Fatalf("forced archive: requests=%d err=%v", requests, err)
	}
}
