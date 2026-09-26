package httpserver

import (
	"database/sql"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

func serveRoot(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	destination := cfg.URLPrefix() + "bookmarks"
	authenticated := false
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if _, err := users.AuthenticateSession(r.Context(), cookie.Value); err == nil {
			authenticated = true
		}
	}
	if !authenticated {
		global, err := settings.LoadGlobal(r.Context(), db)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		if global.LandingPage == "shared_bookmarks" {
			destination = cfg.URLPrefix() + "bookmarks/shared"
		}
	}
	if r.URL.RawQuery != "" {
		destination += "?" + r.URL.RawQuery
	}
	writeRedirect(w, r, destination)
}
