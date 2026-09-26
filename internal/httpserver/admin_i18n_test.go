package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminLanguageNegotiationAndCatalog(t *testing.T) {
	for _, domain := range []string{"core", "auth", "admin"} {
		files, err := adminLocaleFiles.ReadDir("admin_locale/" + domain)
		if err != nil || len(files) < 90 {
			t.Fatalf("%s locale files: %d %v", domain, len(files), err)
		}
		for _, file := range files {
			data, err := adminLocaleFiles.ReadFile("admin_locale/" + domain + "/" + file.Name())
			if err != nil {
				t.Fatal(err)
			}
			entries, err := parseAdminMO(data)
			if err != nil {
				t.Fatalf("%s/%s: %d entries, %v", domain, file.Name(), len(entries), err)
			}
		}
	}
	for _, tc := range []struct {
		name, header, cookie, code, direction string
	}{
		{"default", "", "", "en", "ltr"},
		{"Russian region", "ru-RU,ru;q=0.9,en;q=0.8", "", "ru", "ltr"},
		{"weight", "en;q=0.3,fr;q=0.9", "", "fr", "ltr"},
		{"cookie", "en-US,en;q=0.9", "ru", "ru", "ltr"},
		{"unsupported cookie", "ru", "xx-XX", "ru", "ltr"},
		{"Arabic direction", "ar,en;q=0.5", "", "ar", "rtl"},
		{"disabled preference", "fr;q=0,ru;q=0.5", "", "ru", "ltr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/admin/login/", nil)
			r.Header.Set("Accept-Language", tc.header)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "ld_language", Value: tc.cookie})
			}
			if got := selectedAdminLanguage(r); got.Code != tc.code || got.Dir != tc.direction {
				t.Fatalf("language = %+v, want %s/%s", got, tc.code, tc.direction)
			}
		})
	}
	if got := adminTranslate("ru", "Log in"); got != "Войти" {
		t.Fatalf("Russian catalog: %q", got)
	}
	if got := adminTranslate("fr", "Log in"); got != "Connexion" {
		t.Fatalf("French catalog: %q", got)
	}
	if got := adminTranslate("ru", "linkding administration"); got != "linkding administration" {
		t.Fatalf("unknown text changed: %q", got)
	}
	if _, err := parseAdminMO([]byte("broken")); err == nil {
		t.Fatal("accepted a truncated gettext catalog")
	}
	if _, err := parseAdminMO([]byte(strings.Repeat("x", 28))); err == nil {
		t.Fatal("accepted an invalid gettext catalog magic")
	}
}
