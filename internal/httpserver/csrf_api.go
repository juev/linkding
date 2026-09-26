package httpserver

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed csrf_failure_pages.json
var csrfFailurePagesJSON []byte

var csrfFailurePages = func() map[string]map[string]string {
	var pages map[string]map[string]string
	if err := json.Unmarshal(csrfFailurePagesJSON, &pages); err != nil {
		panic(err)
	}
	return pages
}()

func writeCSRFFailure(w http.ResponseWriter, r *http.Request) {
	pages, ok := csrfFailurePages[selectedAdminLanguage(r).Code]
	if !ok {
		pages = csrfFailurePages["en"]
	}
	key := "without_cookie"
	if _, err := r.Cookie(auth.CSRFCookieName); err == nil {
		key = "with_cookie"
	}
	body := []byte(pages[key])
	setDjangoHTMLHeaders(w, r)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusForbidden)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func verifyAPICSRF(r *http.Request, cfg config.Config) bool {
	cookie, err := r.Cookie(auth.CSRFCookieName)
	if err != nil {
		return false
	}
	token := r.Header.Get("X-CSRFToken")
	if token == "" {
		if err := r.ParseForm(); err != nil {
			return false
		}
		token = r.PostForm.Get("csrfmiddlewaretoken")
	}
	if !auth.VerifyCSRF(cookie.Value, token) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return trustedRequestOrigin(origin, r, cfg)
	}
	if r.TLS != nil {
		return trustedRequestOrigin(r.Header.Get("Referer"), r, cfg)
	}
	return true
}

func trustedRequestOrigin(raw string, r *http.Request, cfg config.Config) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if u.Scheme == scheme && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, trusted := range cfg.CSRFTrustedOrigins {
		if strings.EqualFold(strings.TrimSpace(trusted), u.Scheme+"://"+u.Host) {
			return true
		}
	}
	return false
}
