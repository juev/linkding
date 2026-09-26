package httpserver

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

var passwordPageTemplate = template.Must(template.New("password-change").Parse(passwordPageHTML))

type passwordPageData struct {
	Prefix, CSRFToken, Theme, CustomCSSHash, OldError, NewError, ConfirmError string
	CustomCSS, Done, Close, Authenticated, IsSuperuser, EnableSharing         bool
	ToastHTML                                                                 template.HTML
}

func serveChangePassword(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	data := passwordPageData{Prefix: cfg.URLPrefix()}
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
		old, new1, new2 := r.PostForm.Get("old_password"), r.PostForm.Get("new_password1"), r.PostForm.Get("new_password2")
		if new1 != new2 {
			data.ConfirmError = "The two password fields didn’t match."
		} else if new1 == "" {
			data.ConfirmError = "This field is required."
		} else {
			session, _ := r.Cookie(auth.SessionCookieName)
			if err := users.ChangePassword(r.Context(), user.ID, session.Value, old, new1); err != nil {
				var validation auth.PasswordChangeError
				if errors.As(err, &validation) {
					if validation.Field == "old_password" {
						data.OldError = validation.Message
					} else {
						data.ConfirmError = validation.Message
					}
				} else {
					http.Error(w, "Server error", 500)
					return
				}
			} else {
				http.Redirect(w, r, cfg.URLPrefix()+"password-change-done/", http.StatusFound)
				return
			}
		}
	}
	renderPasswordPage(w, r, cfg, db, user, data)
}

func servePasswordChangeDone(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", 405)
		return
	}
	renderPasswordPage(w, r, cfg, db, user, passwordPageData{Prefix: cfg.URLPrefix(), Done: true})
}

func renderPasswordPage(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, data passwordPageData) {
	form, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Theme = form.Get("theme")
	data.CustomCSS = form.Get("custom_css") != ""
	data.Authenticated = true
	data.IsSuperuser = user.IsSuperuser
	data.EnableSharing = form.Get("enable_sharing") != ""
	if data.CustomCSS {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&data.CustomCSSHash)
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
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, data.CSRFToken, r.URL.Path)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	status := 200
	if data.OldError != "" || data.NewError != "" || data.ConfirmError != "" {
		status = 422
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = passwordPageTemplate.Execute(w, data)
	}
}

const passwordPageHTML = `<!DOCTYPE html><html lang="en" data-api-base-url="{{.Prefix}}api/"><head><meta charset="UTF-8"><link rel="icon" href="{{.Prefix}}static/favicon.ico" sizes="48x48"><link rel="icon" href="{{.Prefix}}static/favicon.svg" sizes="any" type="image/svg+xml"><link rel="apple-touch-icon" sizes="180x180" href="{{.Prefix}}static/apple-touch-icon.png"><link rel="mask-icon" href="{{.Prefix}}static/safari-pinned-tab.svg" color="#5856e0"><link rel="manifest" href="{{.Prefix}}manifest.json"><meta name="viewport" content="width=device-width, initial-scale=1.0, minimal-ui"><meta name="description" content="Self-hosted bookmark service"><meta name="author" content="Sascha Ißbrücker"><title>{{if .Close}}Linkding{{else}}{{if .Done}}Password changed{{else}}Change password{{end}} - Linkding{{end}}</title>{{if eq .Theme "light"}}<link href="{{.Prefix}}static/theme-light.css?v=1.47.0" rel="stylesheet" type="text/css">{{else if eq .Theme "dark"}}<link href="{{.Prefix}}static/theme-dark.css?v=1.47.0" rel="stylesheet" type="text/css">{{else}}<link href="{{.Prefix}}static/theme-dark.css?v=1.47.0" rel="stylesheet" type="text/css" media="(prefers-color-scheme: dark)"><link href="{{.Prefix}}static/theme-light.css?v=1.47.0" rel="stylesheet" type="text/css" media="(prefers-color-scheme: light)">{{end}}{{if .CustomCSS}}<link href="{{.Prefix}}custom_css?hash={{.CustomCSSHash}}" rel="stylesheet" type="text/css">{{end}}<meta name="turbo-cache-control" content="no-preview"><script src="{{.Prefix}}static/bundle.js?v=1.47.0"></script></head><body><header class="container">{{.ToastHTML}}<div class="d-flex justify-between"><a href="{{.Prefix}}" class="app-link d-flex align-center"><img class="app-logo" src="{{.Prefix}}static/logo.png" alt="Application logo"><span class="app-name">LINKDING</span></a><nav>{{if .Authenticated}}<div class="hide-md"><a href="{{.Prefix}}bookmarks/new" class="btn btn-primary mr-2">Add bookmark</a><ld-dropdown class="dropdown"><button class="btn btn-link dropdown-toggle" tabindex="0">Bookmarks</button><ul class="menu" role="list" tabindex="-1"><li class="menu-item"><a href="{{.Prefix}}bookmarks" class="menu-link">Active</a></li><li class="menu-item"><a href="{{.Prefix}}bookmarks/archived" class="menu-link">Archived</a></li>{{if .EnableSharing}}<li class="menu-item"><a href="{{.Prefix}}bookmarks/shared" class="menu-link">Shared</a></li>{{end}}<li class="menu-item"><a href="{{.Prefix}}bookmarks?unread=yes" class="menu-link">Unread</a></li><li class="menu-item"><a href="{{.Prefix}}bookmarks?q=!untagged" class="menu-link">Untagged</a></li></ul></ld-dropdown><ld-dropdown class="dropdown"><button class="btn btn-link dropdown-toggle" tabindex="0">Settings</button><ul class="menu" role="list" tabindex="-1"><li class="menu-item"><a href="{{.Prefix}}settings/general" class="menu-link">General</a></li><li class="menu-item"><a href="{{.Prefix}}settings/integrations" class="menu-link">Integrations</a></li>{{if .IsSuperuser}}<li class="menu-item"><a href="{{.Prefix}}admin/" class="menu-link" data-turbo="false">Admin</a></li>{{end}}</ul></ld-dropdown><form class="d-inline" action="{{.Prefix}}logout/" method="post" data-turbo="false"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}"><button type="submit" class="btn btn-link">Logout</button></form></div><div class="show-md"><a href="{{.Prefix}}bookmarks/new" aria-label="Add bookmark" class="btn btn-primary"><svg width="24" height="24"><use href="{{.Prefix}}static/icons.svg?v=1.47.0#plus"></use></svg></a><ld-dropdown class="dropdown dropdown-right"><button class="btn btn-link dropdown-toggle" aria-label="Navigation menu" tabindex="0"><svg width="24" height="24"><use href="{{.Prefix}}static/icons.svg?v=1.47.0#menu"></use></svg></button><ul class="menu" role="list" tabindex="-1"><li class="menu-item"><a href="{{.Prefix}}bookmarks" class="menu-link">Bookmarks</a></li><li class="menu-item"><a href="{{.Prefix}}bookmarks/archived" class="menu-link">Archived bookmarks</a></li>{{if .EnableSharing}}<li class="menu-item"><a href="{{.Prefix}}bookmarks/shared" class="menu-link">Shared bookmarks</a></li>{{end}}<li class="menu-item"><a href="{{.Prefix}}bookmarks?unread=yes" class="menu-link">Unread</a></li><li class="menu-item"><a href="{{.Prefix}}bookmarks?q=!untagged" class="menu-link">Untagged</a></li><div class="divider"></div><li class="menu-item"><a href="{{.Prefix}}settings/general" class="menu-link">Settings</a></li><li class="menu-item"><a href="{{.Prefix}}settings/integrations" class="menu-link">Integrations</a></li>{{if .IsSuperuser}}<li class="menu-item"><a href="{{.Prefix}}admin/" class="menu-link" data-turbo="false">Admin</a></li>{{end}}<div class="divider"></div><li class="menu-item"><form class="d-inline" action="{{.Prefix}}logout/" method="post" data-turbo="false"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}"><button type="submit" class="btn btn-link menu-link">Logout</button></form></li></ul></ld-dropdown></div>{{else}}<a href="{{.Prefix}}login/" class="btn btn-link">Login</a>{{end}}</nav></div></header><div class="content container">{{if .Close}}<script type="application/javascript">window.close()</script><p>You can now close this window.</p>{{else}}<main class="auth-page" aria-labelledby="main-heading"><div class="section-header"><h1 id="main-heading">{{if .Done}}Password Changed{{else}}Change Password{{end}}</h1></div>{{if .Done}}<p class="text-success">Your password was changed successfully.</p>{{else}}<form method="post" action="{{.Prefix}}change-password/"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}"><div class="form-group"><label for="id_old_password" class="form-label">Old password</label><input type="password" name="old_password" id="id_old_password" class="form-input" autocomplete="current-password" required>{{if .OldError}}<div class="form-input-hint is-error">{{.OldError}}</div>{{end}}</div><div class="form-group"><label for="id_new_password1" class="form-label">New password</label><input type="password" name="new_password1" id="id_new_password1" class="form-input" autocomplete="new-password" required>{{if .NewError}}<div class="form-input-hint is-error">{{.NewError}}</div>{{end}}</div><div class="form-group"><label for="id_new_password2" class="form-label">Confirm new password</label><input type="password" name="new_password2" id="id_new_password2" class="form-input" autocomplete="new-password" required>{{if .ConfirmError}}<div class="form-input-hint is-error">{{.ConfirmError}}</div>{{end}}</div><input type="submit" value="Change Password" class="btn btn-primary width-100 mt-4"></form>{{end}}</main>{{end}}</div><div class="modals"></div></body></html>`
