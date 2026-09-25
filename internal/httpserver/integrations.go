package httpserver

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func integrationHeaders(w http.ResponseWriter) {
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.Header().Set("Content-Language", "en")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
}

func manifestTheme(r *http.Request, db *sql.DB, users *auth.Repository) string {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if user, err := users.AuthenticateSession(r.Context(), cookie.Value); err == nil {
			if profile, err := users.GetProfile(r.Context(), user.ID); err == nil {
				return profile.Theme
			}
		}
	}
	var guest sql.NullInt64
	if err := db.QueryRowContext(r.Context(), `SELECT guest_profile_user_id FROM bookmarks_globalsettings ORDER BY id LIMIT 1`).Scan(&guest); err == nil && guest.Valid {
		if profile, err := users.GetProfile(r.Context(), guest.Int64); err == nil {
			return profile.Theme
		}
	}
	return "auto"
}

func serveManifest(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	prefix := cfg.URLPrefix()
	background := "#ffffff"
	if manifestTheme(r, db, users) == "dark" {
		background = "#161822"
	}
	type icon struct {
		Src     string `json:"src"`
		Type    string `json:"type"`
		Sizes   string `json:"sizes"`
		Purpose string `json:"purpose"`
	}
	type shortcut struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	manifest := struct {
		ShortName       string         `json:"short_name"`
		Name            string         `json:"name"`
		Description     string         `json:"description"`
		StartURL        string         `json:"start_url"`
		Display         string         `json:"display"`
		Scope           string         `json:"scope"`
		ThemeColor      string         `json:"theme_color"`
		BackgroundColor string         `json:"background_color"`
		Icons           []icon         `json:"icons"`
		Shortcuts       []shortcut     `json:"shortcuts"`
		Screenshots     []any          `json:"screenshots"`
		ShareTarget     map[string]any `json:"share_target"`
	}{
		ShortName: "linkding", Name: "linkding", Description: "Self-hosted bookmark service",
		StartURL: "bookmarks", Display: "standalone", Scope: prefix,
		ThemeColor: "#5856e0", BackgroundColor: background,
		Icons: []icon{
			{prefix + "static/logo.svg", "image/svg+xml", "512x512", "any"},
			{prefix + "static/logo-512.png", "image/png", "512x512", "any"},
			{prefix + "static/logo-192.png", "image/png", "192x192", "any"},
			{prefix + "static/maskable-logo.svg", "image/svg+xml", "512x512", "maskable"},
			{prefix + "static/maskable-logo-512.png", "image/png", "512x512", "maskable"},
			{prefix + "static/maskable-logo-192.png", "image/png", "192x192", "maskable"},
		},
		Shortcuts: []shortcut{
			{"Add bookmark", prefix + "bookmarks/new"}, {"Archived", prefix + "bookmarks/archived"},
			{"Unread", prefix + "bookmarks?unread=yes"}, {"Untagged", prefix + "bookmarks?q=!untagged"},
			{"Shared", prefix + "bookmarks/shared"},
		},
		Screenshots: []any{map[string]string{"src": prefix + "static/linkding-screenshot.png", "type": "image/png", "sizes": "2158x1160", "form_factor": "wide"}},
		ShareTarget: map[string]any{
			"action": prefix + "bookmarks/new", "method": "GET", "enctype": "application/x-www-form-urlencoded",
			"params": map[string]string{"url": "url", "text": "url", "title": "title"},
		},
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(manifest)
}

func serveOpenSearch(w http.ResponseWriter, r *http.Request, path string, cfg config.Config) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	base := scheme + "://" + r.Host + cfg.URLPrefix()
	bookmarksURL := strings.TrimSuffix(base, "/") + "/bookmarks"
	integrationHeaders(w)
	w.Header().Set("Content-Type", "application/opensearchdescription+xml")
	_, _ = fmt.Fprintf(w, `<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/" xmlns:moz="http://www.mozilla.org/2006/browser/search/">
    <ShortName>Linkding</ShortName>
    <Description>Linkding</Description>
    <InputEncoding>UTF-8</InputEncoding>
    <Image width="16" height="16" type="image/x-icon">%sstatic/favicon.ico</Image>
    <Url type="text/html" template="%s?client=opensearch&amp;q={searchTerms}"/>
</OpenSearchDescription>
`, html.EscapeString(base), html.EscapeString(bookmarksURL))
}

func serveCustomCSS(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	var ownerID sql.NullInt64
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if user, err := users.AuthenticateSession(r.Context(), cookie.Value); err == nil {
			ownerID = sql.NullInt64{Int64: user.ID, Valid: true}
		}
	}
	if !ownerID.Valid {
		_ = db.QueryRowContext(r.Context(), `SELECT guest_profile_user_id FROM bookmarks_globalsettings ORDER BY id LIMIT 1`).Scan(&ownerID)
	}
	css := ""
	if ownerID.Valid {
		query := `SELECT custom_css FROM bookmarks_userprofile WHERE user_id = ?`
		if cfg.DBEngine == "postgres" {
			query = `SELECT custom_css FROM bookmarks_userprofile WHERE user_id = $1`
		}
		_ = db.QueryRowContext(r.Context(), query, ownerID.Int64).Scan(&css)
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/css")
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	_, _ = fmt.Fprint(w, css)
}
