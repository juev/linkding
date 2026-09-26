package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/juev/linkding/internal/auth"
)

func TestPinnedCSRFFailurePages(t *testing.T) {
	for _, tc := range []struct {
		language string
		cookie   bool
		sha256   string
	}{
		{"en", false, "5f605387ee2a48a64734b723ed0ccb4b232943fb1883cb26c654f3b7314b2ed7"},
		{"en", true, "41888be6bf5e74168d7e52fccaf961789dd9242b35a262be2f1b3c36e4cce629"},
		{"ru", false, "ff2d1389c8619af323cb4aba03d74e876cd97b31dee2466d26cb9a6a3b81edd2"},
		{"ru", true, "786e35b3af0c9a99b155188a3a6d288447f3280ae75c64221284508f205cd88f"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/login/", nil)
		request.Header.Set("Accept-Language", tc.language)
		if tc.cookie {
			request.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "fixture"})
		}
		response := httptest.NewRecorder()
		writeCSRFFailure(response, request)
		digest := sha256.Sum256(response.Body.Bytes())
		if response.Code != http.StatusForbidden || hex.EncodeToString(digest[:]) != tc.sha256 ||
			response.Header().Get("Content-Language") != tc.language ||
			response.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
			response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
			t.Errorf("%s cookie=%t: status=%d headers=%v sha256=%x", tc.language, tc.cookie, response.Code, response.Header(), digest)
		}
	}
}
