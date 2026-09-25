package httpserver

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/importexport"
)

func settingsSession(w http.ResponseWriter, r *http.Request, path string, users *auth.Repository, cfg config.Config) (auth.User, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil {
		user, err := users.AuthenticateSession(r.Context(), cookie.Value)
		if err == nil {
			return user, true
		}
		if err != auth.ErrInvalidCredentials {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return auth.User{}, false
		}
	}
	http.Redirect(w, r, cfg.URLPrefix()+"login/?next="+url.QueryEscape(path), http.StatusFound)
	return auth.User{}, false
}

func serveBookmarkImport(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	redirect := cfg.URLPrefix() + "settings/general"
	if r.Method != http.MethodPost {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	limit := cfg.RequestMaxContentLength
	if limit <= 0 {
		limit = 100 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseMultipartForm(32 << 20); err != nil || !verifyAPICSRF(r, cfg) {
		if err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
		} else {
			http.Error(w, "CSRF verification failed", http.StatusForbidden)
		}
		return
	}
	file, _, err := r.FormFile("import_file")
	if err != nil {
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_error", "Please select a file to import.")
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || !utf8.Valid(content) {
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_error", "An error occurred during bookmark import.")
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	result, err := importexport.ImportNetscape(r.Context(), db, cfg, user.ID, string(content), importexport.ImportOptions{
		MapPrivateFlag: r.PostForm.Get("map_private_flag") == "on",
	})
	if err != nil {
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_error", "An error occurred during bookmark import.")
	} else {
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", fmt.Sprintf("%d bookmarks were successfully imported.", result.Success))
		if result.Failed > 0 {
			settingsFlash(w, cfg.URLPrefix(), "ld_settings_error", fmt.Sprintf("%d bookmarks could not be imported. Please check the logs for more details.", result.Failed))
		}
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func serveBookmarkExport(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	items, err := importexport.LoadForExport(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="bookmarks_%s.html"`, time.Now().UTC().Format("2006-01-02_15-04-05")))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, importexport.ExportNetscape(items))
}
