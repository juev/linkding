package httpserver

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func authProxyMiddleware(next http.Handler, cfg config.Config, users *auth.Repository) http.Handler {
	if !cfg.EnableAuthProxy {
		return next
	}
	header := cfg.AuthProxyUsernameHeader
	if header == "" {
		header = "REMOTE_USER"
	}
	requestHeader := header
	if strings.HasPrefix(header, "HTTP_") {
		requestHeader = strings.ReplaceAll(strings.TrimPrefix(header, "HTTP_"), "_", "-")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get(requestHeader)
		if username == "" && header == "REMOTE_USER" {
			username = r.Header.Get("Remote-User")
		}
		cookie, cookieErr := r.Cookie(auth.SessionCookieName)
		if username == "" {
			if cookieErr == nil {
				if err := users.DeleteSession(r.Context(), cookie.Value); err != nil {
					http.Error(w, "Server error", 500)
					return
				}
				clearProxySession(w, cfg.URLPrefix())
				r = proxyRequestWithSession(r, "")
			}
			next.ServeHTTP(w, r)
			return
		}
		if cookieErr == nil {
			current, err := users.AuthenticateSession(r.Context(), cookie.Value)
			if err == nil && current.Username == username {
				next.ServeHTTP(w, r)
				return
			}
			if err := users.DeleteSession(r.Context(), cookie.Value); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
		}
		user, err := users.GetOrCreateRemoteUser(r.Context(), username)
		if err != nil {
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				http.Error(w, "Server error", 500)
				return
			}
			clearProxySession(w, cfg.URLPrefix())
			next.ServeHTTP(w, proxyRequestWithSession(r, ""))
			return
		}
		age := cfg.SessionCookieAge
		if age <= 0 {
			age = 1_209_600
		}
		key, err := users.CreateSession(r.Context(), user.ID, time.Duration(age)*time.Second)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: key, Path: cfg.URLPrefix(), MaxAge: age,
			Expires: time.Now().Add(time.Duration(age) * time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		next.ServeHTTP(w, proxyRequestWithSession(r, key))
	})
}

func clearProxySession(w http.ResponseWriter, path string) {
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Path: path, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func proxyRequestWithSession(r *http.Request, key string) *http.Request {
	copy := r.Clone(r.Context())
	var cookies []string
	for _, cookie := range r.Cookies() {
		if cookie.Name != auth.SessionCookieName {
			cookies = append(cookies, cookie.String())
		}
	}
	if key != "" {
		cookies = append(cookies, (&http.Cookie{Name: auth.SessionCookieName, Value: key}).String())
	}
	copy.Header.Set("Cookie", strings.Join(cookies, "; "))
	return copy
}
