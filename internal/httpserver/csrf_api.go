package httpserver

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

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
