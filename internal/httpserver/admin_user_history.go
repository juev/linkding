package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_user_history.html admin_sidebar.html
var adminUserHistoryFile embed.FS
var adminUserHistoryTemplate = adminSidebarTemplate(adminUserHistoryFile, "admin_user_history.html")

type adminUserHistoryData struct {
	Language                                         string
	Prefix, Username, CSRFToken, TargetName, ListURL string
	AppPath, AppLabel, AppSlug, ModelSlug            string
	PluralLabel, ModelSingular, TargetPath           string
	DashboardApps                                    []adminDashboardApp
	Rows                                             []adminTagHistoryRow
}

func serveAdminUserHistory(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := cfg.URLPrefix() + "admin/auth/user/"
	part := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, base), "/history/")
	id, err := strconv.ParseInt(part, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "auth", "user")
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if !permissions.Change && !permissions.View {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	data := adminUserHistoryData{Prefix: cfg.URLPrefix(), Username: user.Username, TargetPath: strconv.FormatInt(id, 10), ListURL: base, AppPath: cfg.URLPrefix() + "admin/auth/", AppLabel: "Authentication and Authorization", AppSlug: "auth", ModelSlug: "user", PluralLabel: "Users", ModelSingular: "user"}
	if err := db.QueryRowContext(r.Context(), `SELECT username FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.TargetName); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	location, err := adminTagLocation(cfg.TimeZone)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	rows, err := db.QueryContext(r.Context(), `SELECT l.action_time,u.username,u.first_name,u.last_name,l.action_flag,l.change_message FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id JOIN auth_user AS u ON u.id=l.user_id WHERE c.app_label = `+assetMarker(cfg.DBEngine, 1)+` AND c.model = `+assetMarker(cfg.DBEngine, 2)+` AND l.object_id = `+assetMarker(cfg.DBEngine, 3)+` ORDER BY l.action_time DESC,l.id DESC LIMIT 100`, "auth", "user", strconv.FormatInt(id, 10))
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

func adminHistoryDate(value time.Time) string {
	months := [...]string{"Jan.", "Feb.", "March", "April", "May", "June", "July", "Aug.", "Sept.", "Oct.", "Nov.", "Dec."}
	hour, minute := value.Hour(), value.Minute()
	clock := ""
	switch {
	case hour == 0 && minute == 0:
		clock = "midnight"
	case hour == 12 && minute == 0:
		clock = "noon"
	default:
		suffix := "a.m."
		if hour >= 12 {
			suffix = "p.m."
		}
		hour %= 12
		if hour == 0 {
			hour = 12
		}
		if minute == 0 {
			clock = fmt.Sprintf("%d %s", hour, suffix)
		} else {
			clock = fmt.Sprintf("%d:%02d %s", hour, minute, suffix)
		}
	}
	return fmt.Sprintf("%s %d, %d, %s", months[value.Month()-1], value.Day(), value.Year(), clock)
}
