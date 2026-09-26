package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPinnedHTMLNotFoundResponses(t *testing.T) {
	staticDir := t.TempDir()
	dataDir := t.TempDir()
	db, err := store.Open(context.Background(), config.Config{DBEngine: "sqlite", DataDir: filepath.Join(dataDir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"root", "/does-not-exist", func(w http.ResponseWriter, r *http.Request) {
			serveRoot(w, r, "/", config.Config{}, nil, nil)
		}},
		{"api-root-unknown", "/api/nope/", func(w http.ResponseWriter, r *http.Request) {
			serveAPIRoot(w, r, "/api/", nil)
		}},
		{"asset", "/assets/999999", func(w http.ResponseWriter, r *http.Request) {
			serveAssetPage(w, r, "/assets/", config.Config{DBEngine: "sqlite", DataDir: filepath.Join(dataDir, "data")}, db, nil)
		}},
		{"static", "/static/missing.css", func(w http.ResponseWriter, r *http.Request) {
			serveStaticFile(w, r, "/static/", staticDir, "")
		}},
	} {
		for _, language := range []struct {
			name   string
			accept string
			cookie string
			want   string
		}{
			{"default", "", "", "en"},
			{"accept-ru", "ru", "", "ru"},
			{"cookie-ru-overrides-accept-en", "en", "ru", "ru"},
		} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(tc.name+"/"+language.name+"/"+method, func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(method, tc.path, nil)
					if language.accept != "" {
						request.Header.Set("Accept-Language", language.accept)
					}
					if language.cookie != "" {
						request.AddCookie(&http.Cookie{Name: "ld_language", Value: language.cookie})
					}
					tc.handler(response, request)
					result := response.Result()
					defer result.Body.Close()
					if result.StatusCode != http.StatusNotFound {
						t.Fatalf("status = %d, want 404", result.StatusCode)
					}
					for name, want := range map[string]string{
						"Content-Type":               "text/html; charset=utf-8",
						"Content-Language":           language.want,
						"Vary":                       "Accept-Language, Cookie",
						"X-Frame-Options":            "DENY",
						"Content-Length":             "179",
						"X-Content-Type-Options":     "nosniff",
						"Referrer-Policy":            "same-origin",
						"Cross-Origin-Opener-Policy": "same-origin",
					} {
						if got := result.Header.Get(name); got != want {
							t.Errorf("%s = %q, want %q", name, got, want)
						}
					}
					wantBody := notFoundHTML
					if method == http.MethodHead {
						wantBody = ""
					}
					if got := response.Body.String(); got != wantBody {
						t.Errorf("body = %q, want %q", got, wantBody)
					}
					if tc.name == "static" && result.Header.Get("Content-Security-Policy") != "sandbox" {
						t.Errorf("Content-Security-Policy = %q, want sandbox", result.Header.Get("Content-Security-Policy"))
					}
				})
			}
		}
	}
}
