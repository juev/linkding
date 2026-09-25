package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchSettingsVersionInfo(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"current", `{"name":"v1.47.0"}`, "1.47.0 (latest)", http.StatusOK},
		{"newer", `{"name":"v1.48.0"}`, "1.47.0 (latest: 1.48.0)", http.StatusOK},
		{"missing name", `{}`, "1.47.0", http.StatusOK},
		{"bad JSON", `{`, "1.47.0", http.StatusOK},
		{"upstream error", `{}`, "1.47.0", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/latest" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			got := fetchSettingsVersionInfo(context.Background(), server.Client(), server.URL+"/latest")
			if got != tc.want {
				t.Fatalf("version = %q, want %q", got, tc.want)
			}
		})
	}
}
