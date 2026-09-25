package httpserver

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

//go:embed settings_integrations.html
var integrationsTemplateFile embed.FS

var integrationsTemplate = template.Must(template.ParseFS(integrationsTemplateFile, "settings_integrations.html"))

type integrationsPageData struct {
	Prefix, CSRFToken, Theme, CustomCSSHash string
	CustomCSS, EnableSharing, IsSuperuser   bool
	Global                                  settings.Global
	Tokens                                  []settings.APIToken
	TokenKey, TokenName, SuccessMessage     string
	FeedKey                                 string
	ServerBookmarklet, ClientBookmarklet    template.URL
	ToastHTML                               template.HTML
}

func serveIntegrations(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
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
	form, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	global, err := settings.LoadGlobal(r.Context(), db)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	tokens, err := settings.ListAPITokens(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	feedKey, err := settings.EnsureFeedToken(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	session, _ := r.Cookie(auth.SessionCookieName)
	tokenKey, tokenName, err := users.TakeAPITokenReveal(r.Context(), session.Value)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	appURL := requestScheme(r) + "://" + r.Host + cfg.URLPrefix() + "bookmarks/new"
	server, client := bookmarklets(appURL)
	data := integrationsPageData{Prefix: cfg.URLPrefix(), CSRFToken: masked, Theme: form.Get("theme"),
		CustomCSS: form.Get("custom_css") != "", EnableSharing: form.Get("enable_sharing") != "", IsSuperuser: user.IsSuperuser,
		Global: global, Tokens: tokens, TokenKey: tokenKey, TokenName: tokenName, FeedKey: feedKey,
		SuccessMessage: takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_api_success"), ServerBookmarklet: server, ClientBookmarklet: client}
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, masked, r.URL.Path)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if data.CustomCSS {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&data.CustomCSSHash)
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	if err := integrationsTemplate.Execute(w, data); err != nil {
		return
	}
}

func serveCreateAPIToken(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	_, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		secret := ""
		if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
			secret = cookie.Value
		}
		if secret == "" {
			var err error
			secret, err = auth.NewCSRFSecret()
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			setCSRFCookie(w, cfg.URLPrefix(), secret)
		}
		masked, err := auth.MaskCSRF(secret)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		integrationHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(apiTokenModal(cfg.URLPrefix(), masked)))
		return
	}
	if r.Method != http.MethodPost {
		http.Redirect(w, r, cfg.URLPrefix()+"settings/integrations", http.StatusFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		http.Error(w, "CSRF verification failed", 403)
		return
	}
	session, _ := r.Cookie(auth.SessionCookieName)
	user, err := users.AuthenticateSession(r.Context(), session.Value)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	key, name, err := settings.CreateAPIToken(r.Context(), db, cfg.DBEngine, user.ID, r.PostForm.Get("name"))
	if err != nil {
		http.Error(w, "Invalid token name", 400)
		return
	}
	if err := users.SetAPITokenReveal(r.Context(), session.Value, key, name); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_api_success", `API token "`+name+`" created successfully`)
	http.Redirect(w, r, cfg.URLPrefix()+"settings/integrations", http.StatusFound)
}

func serveDeleteAPIToken(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
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
		id, err := strconv.ParseInt(r.PostForm.Get("token_id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		name, err := settings.DeleteAPIToken(r.Context(), db, cfg.DBEngine, user.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_api_success", `API token "`+name+`" has been deleted.`)
	}
	http.Redirect(w, r, cfg.URLPrefix()+"settings/integrations", http.StatusFound)
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func bookmarklets(applicationURL string) (template.URL, template.URL) {
	encoded, _ := json.Marshal(applicationURL)
	start := "javascript: (function () { const bookmarkUrl = window.location; let applicationUrl = " + string(encoded) + "; applicationUrl += '?url=' + encodeURIComponent(bookmarkUrl);"
	server := start + " applicationUrl += '&auto_close'; window.open(applicationUrl); })();"
	client := `javascript: (function () { const bookmarkUrl = window.location; const title = document.querySelector('title')?.textContent || document.querySelector("meta[property='og:title']")?.getAttribute('content') || ''; const description = document.querySelector("meta[name='description']")?.getAttribute('content') || document.querySelector("meta[property='og:description']")?.getAttribute('content') || ''; let applicationUrl = ` + string(encoded) + `; applicationUrl += '?url=' + encodeURIComponent(bookmarkUrl); applicationUrl += '&title=' + encodeURIComponent(title); applicationUrl += '&description=' + encodeURIComponent(description); applicationUrl += '&auto_close'; window.open(applicationUrl); })();`
	return template.URL(strings.TrimSpace(server)), template.URL(strings.TrimSpace(client))
}

func apiTokenModal(prefix, token string) string {
	return `<turbo-frame id="api-modal"><form method="post" action="` + template.HTMLEscapeString(prefix+`settings/integrations/create-api-token`) + `" data-turbo-frame="api-section"><input type="hidden" name="csrfmiddlewaretoken" value="` + template.HTMLEscapeString(token) + `"><ld-modal class="modal active" data-close-url="` + template.HTMLEscapeString(prefix+`settings/integrations`) + `" data-turbo-frame="api-modal"><div class="modal-overlay" data-close-modal></div><div class="modal-container" role="dialog" aria-modal="true"><div class="modal-header"><h3>Create API Token</h3></div><div class="modal-body"><div class="form-group"><label class="form-label" for="token-name">Token name</label><input type="text" class="form-input" id="token-name" name="name" placeholder="e.g., Browser Extension, Mobile App" value="API Token" maxlength="128"><p class="form-input-hint">A descriptive name to identify the purpose of the token</p></div></div><div class="modal-footer d-flex justify-between"><button type="button" class="btn btn-wide" data-close-modal>Cancel</button><button type="submit" class="btn btn-primary">Create Token</button></div></div></ld-modal></form></turbo-frame>`
}
