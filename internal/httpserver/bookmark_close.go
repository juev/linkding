package httpserver

import (
	"database/sql"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func serveBookmarkClose(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository, _ *sql.DB) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	if _, ok := settingsSession(w, r, path, users, cfg); !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Linkding</title><link href="` + cfg.URLPrefix() + `static/theme-dark.css?v=1.47.0" rel="stylesheet" media="(prefers-color-scheme: dark)"><link href="` + cfg.URLPrefix() + `static/theme-light.css?v=1.47.0" rel="stylesheet" media="(prefers-color-scheme: light)"></head><body><div class="content container"><script type="application/javascript">window.close()</script><p>You can now close this window.</p></div></body></html>`))
}
