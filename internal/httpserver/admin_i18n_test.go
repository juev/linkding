package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminLanguageNegotiationAndCatalog(t *testing.T) {
	for _, domain := range []string{"core", "auth", "admin", "contenttypes", "sessions", "rest_framework"} {
		files, err := adminLocaleFiles.ReadDir("admin_locale/" + domain)
		minimum := 90
		if domain == "rest_framework" {
			minimum = 60
		}
		if err != nil || len(files) < minimum {
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
	if got := adminTranslate("ru", "Content Types"); got != "Типы содержимого" {
		t.Fatalf("Russian content types catalog: %q", got)
	}
	if got := adminTranslate("ru", "linkding administration"); got != "linkding administration" {
		t.Fatalf("unknown text changed: %q", got)
	}
	if got := adminFormTitle("ru", "Delete tag"); got != "Удалить" {
		t.Fatalf("Russian delete title: %q", got)
	}
	if got := adminFormTitle("ru", "Change history: Tag"); got != "История изменений: Tag" {
		t.Fatalf("Russian history title: %q", got)
	}
	if got := adminFilterTitle("ru", "By is archived"); got != "is archived" {
		t.Fatalf("untranslated Russian filter title: %q", got)
	}
	if got := adminFilterOptionLabel("ru", "tags__id__exact", "Greek"); got != "Greek" {
		t.Fatalf("user tag translated: %q", got)
	}
	if got := adminFilterOptionLabel("ru", "is_archived__exact", "Yes"); got != "Да" {
		t.Fatalf("Russian boolean filter option: %q", got)
	}
	if got := adminBulkDeletePrompt("ru", "пользователь"); got != "Вы уверены, что хотите удалить пользователь? Все следующие объекты и связанные с ними элементы будут удалены:" {
		t.Fatalf("Russian bulk deletion prompt: %q", got)
	}
	nodes := []adminDeletionNode{{Label: "User", Children: []adminDeletionNode{{Label: "Log entry", Repr: "Added “Toast object (1)”."}}}}
	localizeAdminDeletionGraph("ru", nil, nodes)
	if got := nodes[0].Children[0].Repr; got != "Добавлено “Toast object (1)“." {
		t.Fatalf("Russian deletion log: %q", got)
	}
	if got := string(adminPasswordPrompt("ru", `<script>alert(1)</script>`)); got != `Введите новый пароль для пользователя <strong>&lt;script&gt;alert(1)&lt;/script&gt;</strong>.` {
		t.Fatalf("escaped Russian password prompt: %q", got)
	}
	if got := adminRelatedTitle("en", "Add another", "user"); got != "Add another user" {
		t.Fatalf("English related title: %q", got)
	}
	if got := adminRelatedTitle("ru", "Add another", "user"); got != "Добавить ещё один объект типа " {
		t.Fatalf("Russian related title: %q", got)
	}
	if got := adminPermissionLabel("ru", "Content Types | content type | Can add content type"); got != "Типы содержимого | тип содержимого | Can add content type" {
		t.Fatalf("Russian permission label: %q", got)
	}
	if _, err := parseAdminMO([]byte("broken")); err == nil {
		t.Fatal("accepted a truncated gettext catalog")
	}
	if _, err := parseAdminMO([]byte(strings.Repeat("x", 28))); err == nil {
		t.Fatal("accepted an invalid gettext catalog magic")
	}
}
