package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestMalformedTokenHeaderMatchesDRF(t *testing.T) {
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
	handler := New(db, cfg, t.TempDir())
	paths := []string{"/api/", "/api/bookmarks/", "/api/tags/", "/api/bundles/", "/api/user/profile/"}
	cases := []struct {
		header string
		detail string
	}{
		{"Token", "Invalid token header. No credentials provided."},
		{"Bearer", "Invalid token header. No credentials provided."},
		{"Token abc def", "Invalid token header. Token string should not contain spaces."},
		{"Bearer abc def", "Invalid token header. Token string should not contain spaces."},
	}
	for _, path := range paths {
		for _, tc := range cases {
			t.Run(path+"/"+tc.header, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Authorization", tc.header)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				var body struct {
					Detail string `json:"detail"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusUnauthorized || body.Detail != tc.detail {
					t.Fatalf("status=%d detail=%q, want 401 %q", response.Code, body.Detail, tc.detail)
				}
			})
		}
	}
}
