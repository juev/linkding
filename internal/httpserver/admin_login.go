package httpserver

import (
	"embed"
	"html/template"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_login.html
var adminLoginFile embed.FS
var adminLoginTemplate = template.Must(template.ParseFS(adminLoginFile, "admin_login.html"))

type adminLoginData struct {
	Prefix, Action, CSRFToken, Next, Username, LoggedInAs string
	Error                                                 bool
}

func serveAdminLogin(w http.ResponseWriter, r *http.Request, cfg config.Config, users *auth.Repository) {
	root := cfg.URLPrefix() + "admin/"
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var current auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		current, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if r.Method == http.MethodGet && current.ID != 0 && current.IsActive && current.IsStaff {
		http.Redirect(w, r, root, http.StatusFound)
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
	data := adminLoginData{Prefix: cfg.URLPrefix(), Action: r.URL.RequestURI(), Next: r.URL.Query().Get("next")}
	if current.ID != 0 && !current.IsStaff {
		data.LoggedInAs = current.Username
	}
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
		user, err := users.AuthenticatePassword(r.Context(), data.Username, r.PostForm.Get("password"))
		if err == nil && user.IsStaff {
			if err := establishLoginSession(w, r, cfg, users, user); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, safeNext(data.Next, r, cfg.URLPrefix()+"bookmarks"), http.StatusFound)
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
	w.WriteHeader(http.StatusOK)
	_ = adminLoginTemplate.Execute(w, data)
}
