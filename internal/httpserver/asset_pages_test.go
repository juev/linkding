package httpserver

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAssetPageAccessAndReader(t *testing.T) {
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
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "page-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "page-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := bookmarks.NewRepository(db, "sqlite").CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://example.com/reader", Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(cfg.DataDir, "assets", "reader.html.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zip := gzip.NewWriter(file)
	if _, err := zip.Write([]byte("<html><title>Fixture</title><body>Readable fixture</body></html>")); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (?, 'reader.html.gz', 100, 'snapshot', 'text/html', 'HTML snapshot', 'complete', 1, ?) RETURNING id`, time.Now().UTC(), item.ID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	aliceKey, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bobKey, err := users.CreateSession(ctx, bob.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	base := "/assets/" + strconv.FormatInt(assetID, 10)
	call := func(path, session string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if session != "" {
			request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if got := call(base, aliceKey); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Readable fixture") || got.Header().Get("Content-Security-Policy") != "sandbox allow-scripts" {
		t.Fatalf("owner asset: %d %q %q", got.Code, got.Body.String(), got.Header().Get("Content-Security-Policy"))
	}
	if got := call(base, bobKey); got.Code != http.StatusNotFound {
		t.Fatalf("shared without enabled profile: %d", got.Code)
	}
	if got := call(base, ""); got.Code != http.StatusNotFound {
		t.Fatalf("private guest asset: %d", got.Code)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_sharing = 1, theme = 'dark' WHERE user_id = ?", alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := call(base, bobKey); got.Code != http.StatusOK {
		t.Fatalf("shared authenticated asset: %d", got.Code)
	}
	reader := call(base+"/read", aliceKey)
	if reader.Code != http.StatusOK || !strings.Contains(reader.Body.String(), "Readable fixture") || !strings.Contains(reader.Body.String(), "theme-dark.css?v=1.47.0") || !strings.Contains(reader.Body.String(), "vendor/Readability.js") || reader.Header().Get("Content-Security-Policy") != "sandbox allow-scripts" {
		t.Fatalf("reader page: %d %q", reader.Code, reader.Body.String())
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_public_sharing = 1 WHERE user_id = ?", alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := call(base, ""); got.Code != http.StatusOK {
		t.Fatalf("public guest asset: %d", got.Code)
	}
}
