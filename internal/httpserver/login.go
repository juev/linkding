package httpserver

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

type loginData struct {
	Prefix       string
	CSRFToken    string
	Next         string
	Username     string
	Error        bool
	DisableLogin bool
	EnableOIDC   bool
}

func serveLogin(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, repo *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost && r.Method != http.MethodOptions {
		w.Header().Set("Allow", "GET, POST, PUT, HEAD, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if _, err := repo.AuthenticateSession(r.Context(), cookie.Value); err == nil {
			writeRedirect(w, r, safeNext(r.URL.Query().Get("next"), r, cfg.URLPrefix()+"bookmarks"))
			return
		}
	}
	if r.Method == http.MethodOptions {
		writeDjangoOptions(w, "GET, POST, PUT, HEAD, OPTIONS")
		return
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		var err error
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	data := loginData{Prefix: cfg.URLPrefix(), Next: r.URL.Query().Get("next"), DisableLogin: cfg.DisableLoginForm, EnableOIDC: cfg.EnableOIDC}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		csrfCookie, err := r.Cookie(auth.CSRFCookieName)
		if err != nil || !auth.VerifyCSRF(csrfCookie.Value, r.PostForm.Get("csrfmiddlewaretoken")) {
			http.Error(w, "CSRF verification failed", http.StatusForbidden)
			return
		}
		data.Username = r.PostForm.Get("username")
		data.Next = r.PostForm.Get("next")
		user, err := repo.AuthenticatePassword(r.Context(), data.Username, r.PostForm.Get("password"))
		if err == nil {
			if err := establishLoginSession(w, r, cfg, repo, user); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			writeRedirect(w, r, safeNext(data.Next, r, cfg.URLPrefix()+"bookmarks"))
			return
		}
		data.Error = true
	}
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = masked
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	status := http.StatusOK
	if data.Error {
		status = http.StatusUnauthorized
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = loginTemplate.Execute(w, data)
	}
}

func establishLoginSession(w http.ResponseWriter, r *http.Request, cfg config.Config, repo *auth.Repository, user auth.User) error {
	age := cfg.SessionCookieAge
	if age == 0 {
		age = 1_209_600
	}
	key, err := repo.CreateSession(r.Context(), user.ID, time.Duration(age)*time.Second)
	if err != nil {
		return err
	}
	if err := repo.RecordLogin(r.Context(), user.ID); err != nil {
		_ = repo.DeleteSession(r.Context(), key)
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: key, Path: cfg.URLPrefix(), MaxAge: age, Expires: time.Now().Add(time.Duration(age) * time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if newSecret, err := auth.NewCSRFSecret(); err == nil {
		setCSRFCookie(w, cfg.URLPrefix(), newSecret)
	}
	return nil
}

func serveLogout(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, repo *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	if r.Method == http.MethodOptions {
		writeDjangoOptions(w, "POST, OPTIONS")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	csrfCookie, err := r.Cookie(auth.CSRFCookieName)
	if err != nil || !auth.VerifyCSRF(csrfCookie.Value, r.PostForm.Get("csrfmiddlewaretoken")) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}
	if sessionCookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if err := repo.DeleteSession(r.Context(), sessionCookie.Value); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Path: cfg.URLPrefix(), MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	destination := cfg.URLPrefix() + "login"
	if cfg.EnableAuthProxy && cfg.AuthProxyLogoutURL != "" {
		destination = cfg.AuthProxyLogoutURL
	}
	writeRedirect(w, r, destination)
}

func setCSRFCookie(w http.ResponseWriter, path, secret string) {
	http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: secret, Path: path, MaxAge: 31_449_600, Expires: time.Now().Add(31_449_600 * time.Second), SameSite: http.SameSiteLaxMode})
}

func safeNext(candidate string, r *http.Request, fallback string) string {
	if candidate == "" || strings.Contains(candidate, "\\") || strings.HasPrefix(candidate, "//") {
		return fallback
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return fallback
	}
	if u.IsAbs() {
		if (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) {
			return fallback
		}
	}
	return candidate
}

const loginHTML = `<!DOCTYPE html>
<html lang="en" data-api-base-url="{{.Prefix}}api/">
<head>
  <meta charset="UTF-8">
  <link rel="icon" href="{{.Prefix}}static/favicon.ico" sizes="48x48">
  <link rel="icon" href="{{.Prefix}}static/favicon.svg" sizes="any" type="image/svg+xml">
  <link rel="apple-touch-icon" sizes="180x180" href="{{.Prefix}}static/apple-touch-icon.png">
  <link rel="mask-icon" href="{{.Prefix}}static/safari-pinned-tab.svg" color="#5856e0">
  <link rel="manifest" href="{{.Prefix}}manifest.json">
  <link rel="search" type="application/opensearchdescription+xml" title="Linkding" href="{{.Prefix}}opensearch.xml">
  <meta name="apple-mobile-web-app-capable" content="yes">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, minimal-ui">
  <meta name="description" content="Self-hosted bookmark service">
  <meta name="robots" content="index,follow">
  <meta name="author" content="Sascha Ißbrücker">
  <title>Login - Linkding</title>
  <link href="{{.Prefix}}static/theme-dark.css?v=1.47.0" rel="stylesheet" type="text/css" media="(prefers-color-scheme: dark)">
  <link href="{{.Prefix}}static/theme-light.css?v=1.47.0" rel="stylesheet" type="text/css" media="(prefers-color-scheme: light)">
  <meta name="theme-color" media="(prefers-color-scheme: dark)" content="#161822">
  <meta name="theme-color" media="(prefers-color-scheme: light)" content="#5856e0">
  <meta name="turbo-cache-control" content="no-preview">
  <meta name="turbo-prefetch" content="false">
  <script src="{{.Prefix}}static/bundle.js?v=1.47.0"></script>
</head>
<body>
  <header class="container">
    <div class="d-flex justify-between">
      <a href="{{.Prefix}}" class="app-link d-flex align-center">
        <img class="app-logo" src="{{.Prefix}}static/logo.png" alt="Application logo"><span class="app-name">LINKDING</span>
      </a>
      <nav><a href="{{.Prefix}}login/" class="btn btn-link">Login</a></nav>
    </div>
  </header>
  <div class="content container">
    <main class="auth-page" aria-labelledby="main-heading">
      <div class="section-header"><h1 id="main-heading">Login</h1></div>
      {{if not .DisableLogin}}
      <form method="post" action="{{.Prefix}}login/">
        <input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}">
        {{if .Error}}<p class="form-input-hint is-error">Your username and password didn't match. Please try again.</p>{{end}}
        <div class="form-group"><label for="id_username" class="form-label">Username</label>
          <input type="text" name="username" value="{{.Username}}" autofocus autocapitalize="none" autocomplete="username" maxlength="150" aria-invalid="false" class="form-input" required id="id_username"></div>
        <div class="form-group"><label for="id_password" class="form-label">Password</label>
          <input type="password" name="password" autocomplete="current-password" aria-invalid="false" class="form-input" required id="id_password"></div>
        <input type="submit" value="Login" class="btn btn-primary width-100 mt-4">
        <input type="hidden" name="next" value="{{.Next}}">
      </form>
      {{end}}
      {{if .EnableOIDC}}<a class="btn width-100 mt-4" href="{{.Prefix}}oidc/authenticate/" data-turbo="false">Login with OIDC</a>{{end}}
    </main>
  </div>
  <div class="modals"></div>
</body>
</html>`
