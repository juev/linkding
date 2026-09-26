package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_user_password.html admin_sidebar.html
var adminUserPasswordFile embed.FS
var adminUserPasswordTemplate = adminSidebarTemplate(adminUserPasswordFile, "admin_user_password.html")

type adminUserPasswordData struct {
	Language                                                      string
	Prefix, Title, Username, Target, CSRFToken, Action, ChangeURL string
	Error                                                         string
	UsablePassword                                                bool
	DashboardApps                                                 []adminDashboardApp
}

func serveAdminUserPassword(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository, actor auth.User, permissions adminPermissions, id int64) {
	if !permissions.Change {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var target, firstName, lastName, email, oldHash string
	query := `SELECT username,first_name,last_name,email,password FROM auth_user WHERE id = ` + assetMarker(cfg.DBEngine, 1)
	err := db.QueryRowContext(r.Context(), query, id).Scan(&target, &firstName, &lastName, &email, &oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	changeURL := cfg.URLPrefix() + "admin/auth/user/" + strconv.FormatInt(id, 10) + "/change/"
	data := adminUserPasswordData{Prefix: cfg.URLPrefix(), Title: "Change password: " + target, Username: actor.Username, Target: target, Action: r.URL.Path, ChangeURL: changeURL, UsablePassword: !strings.HasPrefix(oldHash, "!")}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			http.Error(w, "CSRF verification failed", 403)
			return
		}
		data.UsablePassword = r.PostForm.Get("usable_password") != "false"
		if _, disable := r.PostForm["unset-password"]; disable {
			data.UsablePassword = false
		}
		password1, password2 := r.PostForm.Get("password1"), r.PostForm.Get("password2")
		var encoded string
		if data.UsablePassword {
			if password1 == "" || password2 == "" || password1 != password2 {
				data.Error = "The two password fields didn’t match."
			} else if err := auth.ValidateNewPassword(password2, target, firstName, lastName, email); err != nil {
				data.Error = err.Error()
			} else {
				encoded, err = auth.HashPassword(password2)
				if err != nil {
					http.Error(w, "Server error", 500)
					return
				}
			}
		} else {
			encoded, err = auth.MakeUnusablePassword()
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
		}
		if data.Error == "" {
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			query := `UPDATE auth_user SET password = ` + assetMarker(cfg.DBEngine, 1) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 2)
			if _, err := tx.ExecContext(r.Context(), query, encoded, id); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actor.ID, "auth", "user", strconv.FormatInt(id, 10), target, 2, adminChangeMessage([]string{"password"})); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if actor.ID == id {
				age := cfg.SessionCookieAge
				if age == 0 {
					age = 1_209_600
				}
				key, err := users.CreateSession(r.Context(), id, time.Duration(age)*time.Second)
				if err != nil {
					http.Error(w, "Server error", 500)
					return
				}
				http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: key, Path: cfg.URLPrefix(), MaxAge: age, Expires: time.Now().Add(time.Duration(age) * time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
			}
			http.Redirect(w, r, changeURL, http.StatusFound)
			return
		}
	}
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
	models, err := loadAdminModels(r, db, cfg, actor)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminUserPasswordTemplate.Execute(w, data)
	}
}
