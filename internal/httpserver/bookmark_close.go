package httpserver

import (
	"database/sql"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func serveBookmarkClose(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository, db *sql.DB) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderPasswordPage(w, r, cfg, db, user, passwordPageData{Prefix: cfg.URLPrefix(), Close: true})
}
