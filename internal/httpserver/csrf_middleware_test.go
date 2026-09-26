package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPinnedCSRFMiddlewareRunsBeforeUIViews(t *testing.T) {
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	for _, path := range []string{
		"/", "/bookmarks", "/bookmarks/new", "/tags/new", "/bundles/new",
		"/settings/import", "/admin/login/", "/login/", "/toasts/acknowledge",
		"/health", "/assets/1", "/manifest.json", "/custom_css", "/opensearch.xml", "/feeds/shared",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusForbidden || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Body.Len() != 1367 {
			t.Errorf("POST %s: status=%d type=%q body bytes=%d", path, response.Code, response.Header().Get("Content-Type"), response.Body.Len())
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/does-not-exist", 404}, {"/assets/abc", 404}, {"/static/missing.css", 404}, {"/api/bookmarks/", 401},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, nil))
		if response.Code != tc.status {
			t.Errorf("POST %s: status=%d, want %d", tc.path, response.Code, tc.status)
		}
	}
}
