package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestPinnedAPIDetailTranslations(t *testing.T) {
	for _, tc := range []struct {
		language string
		input    string
		want     string
	}{
		{"ru", "Authentication credentials were not provided.", "Учетные данные не были предоставлены."},
		{"ru", "Invalid token.", "Недействительный токен."},
		{"ru", "Not found.", "Страница не найдена."},
		{"ru", `Method "PATCH" not allowed.`, `Метод "PATCH" не разрешен.`},
		{"fr", `Method "DELETE" not allowed.`, "Méthode «\u00a0DELETE\u00a0» non autorisée."},
		{"en", "Not found.", "Not found."},
		{"ru", "No Bookmark matches the given query.", "No Bookmark matches the given query."},
	} {
		if got := localizedAPIDetail(tc.language, tc.input); got != tc.want {
			t.Errorf("%s %q: got %q, want %q", tc.language, tc.input, got, tc.want)
		}
	}
}

func TestAPIRootLocalizedUnauthorizedResponse(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "/api/", nil)
		request.Header.Set("Accept-Language", "ru")
		response := httptest.NewRecorder()
		serveAPIRoot(response, request, "/api/", nil)
		const body = `{"detail":"Учетные данные не были предоставлены."}`
		if response.Code != http.StatusUnauthorized || response.Header().Get("Content-Language") != "ru" || response.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: status=%d headers=%v", method, response.Code, response.Header())
		}
		if method == http.MethodGet && strings.TrimSpace(response.Body.String()) != body {
			t.Errorf("GET body=%q", response.Body.String())
		}
		if method == http.MethodHead && (response.Body.Len() != 0 || response.Header().Get("Content-Length") != strconv.Itoa(len(body))) {
			t.Errorf("HEAD body=%q length=%q", response.Body.String(), response.Header().Get("Content-Length"))
		}
	}
}
