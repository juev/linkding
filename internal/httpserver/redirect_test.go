package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/juev/linkding/internal/config"
)

func TestPinnedRedirectHeadersAndLoginNext(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/linkding/bookmarks?q=hello%20world&unread=yes", nil)
	request.Header.Set("Accept-Language", "ru")
	response := httptest.NewRecorder()
	redirectToLogin(response, request, config.Config{ContextPath: "linkding/"})
	if response.Code != http.StatusFound || response.Body.Len() != 0 {
		t.Fatalf("redirect: status=%d body=%q", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Location":                   "/linkding/login?next=/linkding/bookmarks%3Fq%3Dhello%2520world%26unread%3Dyes",
		"Content-Type":               "text/html; charset=utf-8",
		"Content-Length":             "0",
		"Content-Language":           "ru",
		"Vary":                       "Accept-Language, Cookie",
		"X-Frame-Options":            "DENY",
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "same-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
