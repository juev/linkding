package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func serveBookmarkSearchPost(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, user auth.User) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		writeCSRFFailure(w, r)
		return
	}
	if _, save := r.PostForm["save"]; save && user.ID == 0 {
		http.Error(w, "Forbidden", 403)
		return
	}
	defaults := map[string]string{"q": "", "user": "", "bundle": "", "sort": "added_desc", "shared": "off", "unread": "off", "modified_since": "", "added_since": ""}
	if user.ID > 0 {
		var raw string
		if err := db.QueryRowContext(r.Context(), `SELECT search_preferences FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&raw); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		var stored map[string]string
		if err := json.Unmarshal([]byte(raw), &stored); err == nil {
			for key, value := range stored {
				defaults[key] = value
			}
		}
	}
	for _, name := range []string{"sort", "shared", "unread"} {
		if value := r.PostForm.Get(name); value != "" {
			if _, save := r.PostForm["save"]; save {
				defaults[name] = value
			}
		}
	}
	if _, save := r.PostForm["save"]; save {
		preferences := map[string]string{"sort": defaults["sort"], "shared": defaults["shared"], "unread": defaults["unread"]}
		encoded, _ := json.Marshal(preferences)
		if _, err := db.ExecContext(r.Context(), `UPDATE bookmarks_userprofile SET search_preferences = `+assetMarker(cfg.DBEngine, 1)+` WHERE user_id = `+assetMarker(cfg.DBEngine, 2), string(encoded), user.ID); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	params := url.Values{}
	for _, name := range []string{"q", "user", "bundle", "sort", "shared", "unread", "modified_since", "added_since"} {
		value := r.PostForm.Get(name)
		if value != "" && value != defaults[name] {
			params.Set(name, value)
		}
	}
	destination := path
	if encoded := params.Encode(); encoded != "" {
		destination += "?" + strings.ReplaceAll(encoded, "+", "%20")
	}
	writeRedirect(w, r, destination)
}
