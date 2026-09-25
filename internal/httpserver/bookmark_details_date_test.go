package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBookmarkDetailsDateMatchesPinnedDjangoLocales(t *testing.T) {
	value := time.Date(2026, 9, 25, 10, 21, 0, 0, time.UTC)
	for _, tc := range []struct {
		language, zone, want string
	}{
		{"en-US", "UTC", "Sept. 25, 2026, 10:21 a.m."},
		{"ru-RU", "UTC", "25 сентября 2026 г. 10:21"},
		{"ru-RU", "Europe/Moscow", "25 сентября 2026 г. 13:21"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
		r.Header.Set("Accept-Language", tc.language)
		if got := formatBookmarkDetailsDate(value, r, tc.zone); got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.language, tc.zone, got, tc.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/bookmarks", nil)
	r.Header.Set("Accept-Language", "ru-RU")
	r.AddCookie(&http.Cookie{Name: "ld_language", Value: "en-us"})
	if got := formatBookmarkDetailsDate(value, r, "UTC"); got != "Sept. 25, 2026, 10:21 a.m." {
		t.Fatalf("language cookie must override request header: %q", got)
	}
}
