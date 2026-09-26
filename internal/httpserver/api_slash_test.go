package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestCanonicalAPISlashRedirect(t *testing.T) {
	for _, contextPath := range []string{"", "linkding/"} {
		t.Run(contextPath, func(t *testing.T) {
			cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: contextPath}
			db, err := store.Open(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := store.Migrate(context.Background(), db, "sqlite"); err != nil {
				t.Fatal(err)
			}
			handler := New(db, cfg, t.TempDir())
			for _, path := range []string{
				"api", "api/bookmarks", "api/bookmarks/1", "api/bookmarks/foo/archive",
				"api/bookmarks/archived", "api/bookmarks/shared", "api/bookmarks/check",
				"api/bookmarks/singlefile", "api/bookmarks/1/assets",
				"api/bookmarks/1/assets/upload", "api/bookmarks/1/assets/foo/download",
				"api/tags", "api/tags/foo", "api/bundles", "api/bundles/1",
				"api/user/profile",
			} {
				for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost} {
					t.Run(method+" "+path, func(t *testing.T) {
						url := "http://linkding.test" + cfg.URLPrefix() + path + "?q=test"
						request := httptest.NewRequest(method, url, nil)
						response := httptest.NewRecorder()
						handler.ServeHTTP(response, request)
						wantLocation := cfg.URLPrefix() + path + "/?q=test"
						if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != wantLocation ||
							response.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
							response.Header().Get("Vary") != "Cookie" || response.Header().Get("Content-Language") != "" ||
							response.Body.Len() != 0 {
							t.Fatalf("%s %s: status=%d Location=%q headers=%v body=%q", method, path, response.Code, response.Header().Get("Location"), response.Header(), response.Body.String())
						}
					})
				}
			}
			for _, path := range []string{"api/user", "api/foo", "api/bookmarks/1/foo", "api/bookmarks/foo/assets", "api/tags/foo.bar"} {
				request := httptest.NewRequest(http.MethodGet, "http://linkding.test"+cfg.URLPrefix()+path, nil)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code == http.StatusMovedPermanently {
					t.Errorf("unknown route %s redirected", path)
				}
			}
		})
	}
}
