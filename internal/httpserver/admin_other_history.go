package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func serveAdminOtherHistory(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, model, objectID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	definition, ok := findAdminModel("bookmarks", model)
	if !ok {
		http.NotFound(w, r)
		return
	}
	permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", model)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if !permissions.Change && !permissions.View {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if model != "feedtoken" {
		if id, err := strconv.ParseInt(objectID, 10, 64); err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
	}
	base := cfg.URLPrefix() + "admin/bookmarks/" + model + "/"
	data := adminUserHistoryData{
		Prefix: cfg.URLPrefix(), Username: user.Username, ListURL: base,
		AppPath: cfg.URLPrefix() + "admin/bookmarks/", AppLabel: "Bookmarks", AppSlug: "bookmarks", ModelSlug: model,
		PluralLabel: definition.Plural, ModelSingular: strings.ToLower(definition.Label), TargetPath: url.PathEscape(objectID),
	}
	if model == "apitoken" {
		data.PluralLabel = "Api tokens"
	}
	var query string
	switch model {
	case "toast":
		query = `SELECT id FROM bookmarks_toast WHERE id = ` + assetMarker(cfg.DBEngine, 1)
		var id int64
		err = db.QueryRowContext(r.Context(), query, objectID).Scan(&id)
		data.TargetName = "Toast object (" + strconv.FormatInt(id, 10) + ")"
	case "apitoken":
		query = `SELECT t.name,u.username FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id WHERE t.id = ` + assetMarker(cfg.DBEngine, 1)
		var name, owner string
		err = db.QueryRowContext(r.Context(), query, objectID).Scan(&name, &owner)
		data.TargetName = name + " (" + owner + ")"
	case "feedtoken":
		query = `SELECT key FROM bookmarks_feedtoken WHERE key = ` + assetMarker(cfg.DBEngine, 1)
		err = db.QueryRowContext(r.Context(), query, objectID).Scan(&data.TargetName)
	case "bookmarkbundle":
		query = `SELECT name FROM bookmarks_bookmarkbundle WHERE id = ` + assetMarker(cfg.DBEngine, 1)
		err = db.QueryRowContext(r.Context(), query, objectID).Scan(&data.TargetName)
	case "bookmarkasset":
		query = `SELECT id,display_name FROM bookmarks_bookmarkasset WHERE id = ` + assetMarker(cfg.DBEngine, 1)
		var id int64
		var displayName string
		err = db.QueryRowContext(r.Context(), query, objectID).Scan(&id, &displayName)
		data.TargetName = adminBookmarkAssetRepr(id, displayName)
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	location, err := adminTagLocation(cfg.TimeZone)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	rows, err := db.QueryContext(r.Context(), `SELECT l.action_time,u.username,u.first_name,u.last_name,l.action_flag,l.change_message FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id JOIN auth_user AS u ON u.id=l.user_id WHERE c.app_label = `+assetMarker(cfg.DBEngine, 1)+` AND c.model = `+assetMarker(cfg.DBEngine, 2)+` AND l.object_id = `+assetMarker(cfg.DBEngine, 3)+` ORDER BY l.action_time DESC,l.id DESC LIMIT 100`, "bookmarks", model, objectID)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	for rows.Next() {
		var happened time.Time
		var entry adminTagHistoryRow
		var first, last, message string
		var flag int
		if err := rows.Scan(&happened, &entry.Username, &first, &last, &flag, &message); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		if full := strings.TrimSpace(first + " " + last); full != "" {
			entry.Username += " (" + full + ")"
		}
		entry.Date = adminHistoryDate(happened.In(location))
		entry.Action = adminTagHistoryMessage(flag, message)
		data.Rows = append(data.Rows, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(w, "Server error", 500)
		return
	}
	rows.Close()
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	models, err := loadAdminModels(r, db, cfg, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminUserHistoryTemplate.Execute(w, data)
	}
}
