package httpserver

import (
	"database/sql"
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/settings"
)

//go:embed settings_general.html
var settingsTemplateFile embed.FS

var settingsTemplate = template.Must(template.ParseFS(settingsTemplateFile, "settings_general.html"))

type settingsOption struct {
	Value, Label string
	Selected     bool
}

type settingsField struct {
	Name, Label, Kind, Value, Class string
	Help                            template.HTML
	Checked, Hidden                 bool
	Options                         []settingsOption
}

type settingsPageData struct {
	Prefix, CSRFToken, Theme, CustomCSSHash string
	CustomCSS                               bool
	IsSuperuser, EnableSharing              bool
	EnableRefreshFavicons, HasSnapshots     bool
	SuccessMessage, ErrorMessage            string
	VersionInfo                             string
	Fields                                  []settingsField
	Global                                  settings.Global
	Users                                   []settings.UserOption
	ToastHTML                               template.HTML
}

func serveSettingsGeneral(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path && r.URL.Path != cfg.URLPrefix()+"settings" {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, r.URL.Path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = renderSettingsGeneral(w, r, cfg, db, user, nil, "", http.StatusOK)
}

func renderSettingsGeneral(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, formOverride url.Values, errorMessage string, status int) error {
	form := formOverride
	var err error
	if form == nil {
		form, err = settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return err
		}
	}
	global, err := settings.LoadGlobal(r.Context(), db)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return err
	}
	var options []settings.UserOption
	if user.IsSuperuser {
		options, err = settings.ListUsers(r.Context(), db)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return err
		}
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return err
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return err
	}
	data := settingsPageData{
		Prefix: cfg.URLPrefix(), CSRFToken: masked, Theme: form.Get("theme"),
		CustomCSS: form.Get("custom_css") != "", EnableSharing: form.Get("enable_sharing") != "",
		IsSuperuser: user.IsSuperuser, EnableRefreshFavicons: cfg.EnableRefreshFavicons,
		HasSnapshots: cfg.EnableSnapshots, Fields: profileDisplayFields(form), Global: global, Users: options,
		ErrorMessage: errorMessage, VersionInfo: settingsVersionInfo(r.Context()),
	}
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, masked, r.URL.Path)
	if err != nil {
		http.Error(w, "Server error", 500)
		return err
	}
	if data.CustomCSS {
		query := `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = ` + assetMarker(cfg.DBEngine, 1)
		_ = db.QueryRowContext(r.Context(), query, user.ID).Scan(&data.CustomCSSHash)
	}
	data.SuccessMessage = takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_settings_success")
	if data.ErrorMessage == "" {
		data.ErrorMessage = takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_settings_error")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	integrationHeaders(w)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return nil
	}
	return settingsTemplate.Execute(w, data)
}

func settingsFlash(w http.ResponseWriter, prefix, name, message string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: base64.RawURLEncoding.EncodeToString([]byte(message)), Path: prefix,
		MaxAge: 60, Expires: time.Now().Add(time.Minute), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func takeSettingsFlash(w http.ResponseWriter, r *http.Request, prefix, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: name, Path: prefix, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return ""
	}
	return string(decoded)
}

func serveSettingsUpdate(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	destination := cfg.URLPrefix() + "settings/general"
	if r.Method != http.MethodPost {
		http.Redirect(w, r, destination, http.StatusFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}
	if _, update := r.PostForm["update_profile"]; update {
		err := settings.UpdateProfileWithTasks(r.Context(), db, cfg.DBEngine, user.ID, r.PostForm, cfg.DisableBackgroundTasks)
		var validation settings.ValidationError
		if errors.As(err, &validation) {
			_ = renderSettingsGeneral(w, r, cfg, db, user, r.PostForm, "Profile update failed, check the form below for errors", 422)
			return
		}
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "Profile updated")
	}
	if _, update := r.PostForm["update_global_settings"]; update {
		if !user.IsSuperuser {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if err := settings.UpdateGlobal(r.Context(), db, cfg.DBEngine, user.IsSuperuser, r.PostForm); err != nil {
			var validation settings.ValidationError
			if errors.As(err, &validation) {
				_ = renderSettingsGeneral(w, r, cfg, db, user, nil, "Global settings update failed", 422)
			} else {
				http.Error(w, "Server error", http.StatusInternalServerError)
			}
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "Global settings updated")
	}
	if _, refresh := r.PostForm["refresh_favicons"]; refresh && cfg.EnableRefreshFavicons {
		// The refresh action is wired to the task queue below.
		if err := jobs.EnqueueRefreshFavicons(r.Context(), db, cfg.DBEngine, user.ID); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "Scheduled favicon update. This may take a while...")
	}
	if _, snapshots := r.PostForm["create_missing_html_snapshots"]; snapshots && cfg.EnableSnapshots {
		count, err := jobs.EnqueueMissingSnapshots(r.Context(), db, cfg.DBEngine, user.ID)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		if count > 0 {
			settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "Queued "+strconv.Itoa(count)+" missing snapshots. This may take a while...")
		} else {
			settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "No missing snapshots found.")
		}
	}
	http.Redirect(w, r, destination, http.StatusFound)
}
