package httpserver

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

const (
	passkeyChallengeCookie = "ld_passkey_challenge"
	passkeyCeremonyTTL     = 5 * time.Minute
	passkeyRequestLimit    = 64 << 10
	passkeyKindLogin       = "login"
	passkeyKindRegister    = "register"
)

// passkeyUser adapts a linkding user to the WebAuthn user interface. The user
// handle is the big-endian user ID, so it carries no personal data.
type passkeyUser struct {
	user        auth.User
	credentials []webauthn.Credential
}

func passkeyUserHandle(id int64) []byte {
	handle := make([]byte, 8)
	binary.BigEndian.PutUint64(handle, uint64(id))
	return handle
}

func (u passkeyUser) WebAuthnID() []byte                         { return passkeyUserHandle(u.user.ID) }
func (u passkeyUser) WebAuthnName() string                       { return u.user.Username }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.user.Username }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func loadPasskeyUser(r *http.Request, users *auth.Repository, user auth.User) (passkeyUser, []auth.Passkey, error) {
	passkeys, err := users.ListPasskeys(r.Context(), user.ID)
	if err != nil {
		return passkeyUser{}, nil, err
	}
	credentials := make([]webauthn.Credential, len(passkeys))
	for i, passkey := range passkeys {
		credentials[i] = passkey.Credential
	}
	return passkeyUser{user: user, credentials: credentials}, passkeys, nil
}

// passkeyHost returns the host the browser used, honoring the same proxy
// headers as the OIDC callback URL.
func passkeyHost(r *http.Request, cfg config.Config) string {
	host := r.Host
	if cfg.UseXForwardedHost {
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
	}
	return host
}

// passkeyRelyingParty derives the relying party from the request host. The
// RP ID is the host name without port; the browser enforces that it matches
// the page origin, and the server accepts that host over HTTP and HTTPS plus
// the configured trusted CSRF origins.
func passkeyRelyingParty(r *http.Request, cfg config.Config) (*webauthn.WebAuthn, error) {
	host := passkeyHost(r, cfg)
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.Trim(hostname, "[]")
	if hostname == "" {
		return nil, fmt.Errorf("request has no host")
	}
	origins := []string{"https://" + host, "http://" + host}
	for _, trusted := range cfg.CSRFTrustedOrigins {
		if trusted = strings.TrimSpace(trusted); trusted != "" {
			origins = append(origins, trusted)
		}
	}
	return webauthn.New(&webauthn.Config{
		RPID:          hostname,
		RPDisplayName: "Linkding",
		RPOrigins:     origins,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyCeremonyTTL, TimeoutUVD: passkeyCeremonyTTL},
		},
	})
}

func writePasskeyJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = writeJSON(w, value)
}

func writePasskeyError(w http.ResponseWriter, status int, message string) {
	writePasskeyJSON(w, status, map[string]string{"error": message})
}

func passkeyPostOnly(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writePasskeyError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return false
	}
	return true
}

func setPasskeyChallengeCookie(w http.ResponseWriter, r *http.Request, cfg config.Config, key string) {
	http.SetCookie(w, &http.Cookie{Name: passkeyChallengeCookie, Value: key, Path: cfg.URLPrefix(), MaxAge: int(passkeyCeremonyTTL / time.Second),
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}

// takePasskeyChallenge consumes the ceremony state named by the cookie.
func takePasskeyChallenge(w http.ResponseWriter, r *http.Request, cfg config.Config, users *auth.Repository, kind string, userID int64) (webauthn.SessionData, error) {
	http.SetCookie(w, &http.Cookie{Name: passkeyChallengeCookie, Path: cfg.URLPrefix(), MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	var session webauthn.SessionData
	cookie, err := r.Cookie(passkeyChallengeCookie)
	if err != nil {
		return session, auth.ErrInvalidChallenge
	}
	data, err := users.TakeWebAuthnChallenge(r.Context(), cookie.Value, kind, userID)
	if err != nil {
		return session, err
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return session, auth.ErrInvalidChallenge
	}
	return session, nil
}

func savePasskeyChallenge(w http.ResponseWriter, r *http.Request, cfg config.Config, users *auth.Repository, kind string, userID int64, session *webauthn.SessionData) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	expires := session.Expires
	if expires.IsZero() {
		expires = time.Now().Add(passkeyCeremonyTTL)
	}
	key, err := users.SaveWebAuthnChallenge(r.Context(), kind, userID, data, expires)
	if err != nil {
		return err
	}
	setPasskeyChallengeCookie(w, r, cfg, key)
	return nil
}

// passkeySessionUser authenticates JSON settings requests without the HTML
// login redirect that settingsSession writes.
func passkeySessionUser(w http.ResponseWriter, r *http.Request, users *auth.Repository) (auth.User, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil {
		user, err := users.AuthenticateSession(r.Context(), cookie.Value)
		if err == nil {
			return user, true
		}
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			logServerError(r, err)
			writePasskeyError(w, http.StatusInternalServerError, "Server error.")
			return auth.User{}, false
		}
	}
	writePasskeyError(w, http.StatusUnauthorized, "Please sign in again.")
	return auth.User{}, false
}

func servePasskeyLoginBegin(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if !passkeyPostOnly(w, r, path) {
		return
	}
	rp, err := passkeyRelyingParty(r, cfg)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Passkeys are not available for this host name.")
		return
	}
	assertion, session, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	if err := savePasskeyChallenge(w, r, cfg, users, passkeyKindLogin, 0, session); err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	writePasskeyJSON(w, http.StatusOK, assertion)
}

func servePasskeyLoginFinish(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if !passkeyPostOnly(w, r, path) {
		return
	}
	rp, err := passkeyRelyingParty(r, cfg)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Passkeys are not available for this host name.")
		return
	}
	session, err := takePasskeyChallenge(w, r, cfg, users, passkeyKindLogin, 0)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidChallenge) {
			logServerError(r, err)
		}
		writePasskeyError(w, http.StatusBadRequest, "The passkey request expired. Please try again.")
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(http.MaxBytesReader(w, r.Body, passkeyRequestLimit))
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Invalid passkey response.")
		return
	}
	var owner auth.User
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		user, err := users.PasskeyOwner(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(userHandle, passkeyUserHandle(user.ID)) {
			return nil, auth.ErrInvalidCredentials
		}
		account, _, err := loadPasskeyUser(r, users, user)
		if err != nil {
			return nil, err
		}
		owner = user
		return account, nil
	}
	_, credential, err := rp.ValidatePasskeyLogin(handler, session, parsed)
	if err != nil || credential == nil {
		writePasskeyError(w, http.StatusUnauthorized, "Passkey sign-in failed.")
		return
	}
	if credential.Authenticator.CloneWarning {
		logServerError(r, fmt.Errorf("passkey sign counter went backwards for user %d; possible cloned authenticator", owner.ID))
		writePasskeyError(w, http.StatusUnauthorized, "Passkey sign-in failed.")
		return
	}
	if err := users.RecordPasskeyLogin(r.Context(), owner.ID, *credential); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writePasskeyError(w, http.StatusUnauthorized, "Passkey sign-in failed.")
			return
		}
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	if err := establishLoginSession(w, r, cfg, users, owner); err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	writePasskeyJSON(w, http.StatusOK, map[string]string{"redirect": safeNext(r.URL.Query().Get("next"), r, cfg.URLPrefix()+"bookmarks")})
}

func servePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if !passkeyPostOnly(w, r, path) {
		return
	}
	user, ok := passkeySessionUser(w, r, users)
	if !ok {
		return
	}
	rp, err := passkeyRelyingParty(r, cfg)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Passkeys are not available for this host name.")
		return
	}
	account, _, err := loadPasskeyUser(r, users, user)
	if err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	exclusions := make([]protocol.CredentialDescriptor, len(account.credentials))
	for i := range account.credentials {
		exclusions[i] = account.credentials[i].Descriptor()
	}
	creation, session, err := rp.BeginRegistration(account,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclusions))
	if err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	if err := savePasskeyChallenge(w, r, cfg, users, passkeyKindRegister, user.ID, session); err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	writePasskeyJSON(w, http.StatusOK, creation)
}

func servePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if !passkeyPostOnly(w, r, path) {
		return
	}
	user, ok := passkeySessionUser(w, r, users)
	if !ok {
		return
	}
	rp, err := passkeyRelyingParty(r, cfg)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Passkeys are not available for this host name.")
		return
	}
	session, err := takePasskeyChallenge(w, r, cfg, users, passkeyKindRegister, user.ID)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidChallenge) {
			logServerError(r, err)
		}
		writePasskeyError(w, http.StatusBadRequest, "The passkey request expired. Please try again.")
		return
	}
	var body struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, passkeyRequestLimit))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		writePasskeyError(w, http.StatusBadRequest, "Invalid passkey response.")
		return
	}
	name, err := auth.NormalizePasskeyName(body.Name)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "The passkey name must have at most 128 characters.")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body.Credential)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "Invalid passkey response.")
		return
	}
	account, _, err := loadPasskeyUser(r, users, user)
	if err != nil {
		logServerError(r, err)
		writePasskeyError(w, http.StatusInternalServerError, "Server error.")
		return
	}
	credential, err := rp.CreateCredential(account, session, parsed)
	if err != nil {
		writePasskeyError(w, http.StatusBadRequest, "The passkey could not be verified.")
		return
	}
	if err := users.AddPasskey(r.Context(), user.ID, name, *credential); err != nil {
		writePasskeyError(w, http.StatusBadRequest, "This passkey is already registered.")
		return
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", `Passkey "`+name+`" added.`)
	writePasskeyJSON(w, http.StatusOK, map[string]string{"redirect": cfg.URLPrefix() + "settings/general"})
}

func servePasskeyDelete(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	redirect := cfg.URLPrefix() + "settings/general"
	if r.Method != http.MethodPost {
		writeRedirect(w, r, redirect)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PostForm.Get("passkey_id"), 10, 64)
	if err != nil || id <= 0 {
		writeNotFound(w, r)
		return
	}
	name, err := users.DeletePasskey(r.Context(), user.ID, id, cfg.DisablePasswordLogin)
	switch {
	case errors.Is(err, auth.ErrPasskeyNotFound):
		writeNotFound(w, r)
		return
	case errors.Is(err, auth.ErrLastPasskey):
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_error", "You cannot delete your last passkey while password sign-in is disabled.")
	case err != nil:
		logServerError(r, err)
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	default:
		settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", `Passkey "`+name+`" deleted.`)
	}
	writeRedirect(w, r, redirect)
}

var passkeyRecoveryTemplate = parsePageString("passkey-recovery", passkeyRecoveryHTML)

type passkeyRecoveryData struct {
	Prefix, CSRFToken, Token string
	Invalid                  bool
}

// servePasskeyRecovery signs a user in with a link from the passkey_recovery
// command. GET only shows a confirmation form, so link previews and prefetches
// do not consume the single-use token.
func servePasskeyRecovery(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, users *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		var err error
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	data := passkeyRecoveryData{Prefix: cfg.URLPrefix(), Token: r.URL.Query().Get("token")}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		user, err := users.ConsumeRecoveryToken(r.Context(), r.PostForm.Get("token"))
		if err == nil {
			if err := establishLoginSession(w, r, cfg, users, user); err != nil {
				logServerError(r, err)
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			settingsFlash(w, cfg.URLPrefix(), "ld_settings_success", "Signed in with a recovery link. Add a new passkey below.")
			writeRedirect(w, r, cfg.URLPrefix()+"settings/general")
			return
		}
		if !errors.Is(err, auth.ErrInvalidRecoveryToken) {
			logServerError(r, err)
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		data.Invalid = true
		data.Token = ""
		status = http.StatusBadRequest
	}
	if data.Token == "" {
		data.Invalid = true
	}
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	data.CSRFToken = masked
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	// same-origin keeps the token out of cross-site Referer headers. The
	// stricter no-referrer makes browsers send "Origin: null" with the form,
	// which the CSRF origin check rejects.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = passkeyRecoveryTemplate.Execute(w, data)
	}
}

// passkeyListItem is one row of the settings passkey table.
type passkeyListItem struct {
	ID                      int64
	Name, Created, LastUsed string
}

func passkeyListItems(passkeys []auth.Passkey) []passkeyListItem {
	items := make([]passkeyListItem, len(passkeys))
	for i, passkey := range passkeys {
		items[i] = passkeyListItem{ID: passkey.ID, Name: passkey.Name, Created: passkey.CreatedAt.UTC().Format("Jan 02, 2006 15:04")}
		if passkey.LastUsedAt.Valid {
			items[i].LastUsed = passkey.LastUsedAt.Time.UTC().Format("Jan 02, 2006 15:04")
		}
	}
	return items
}

func listPasskeyItems(r *http.Request, db *sql.DB, cfg config.Config, userID int64) ([]passkeyListItem, error) {
	passkeys, err := auth.NewRepository(db, cfg.DBEngine).ListPasskeys(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	return passkeyListItems(passkeys), nil
}

const passkeyRecoveryHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <link rel="icon" href="{{.Prefix}}static/favicon.ico" sizes="48x48">
  <link rel="icon" href="{{.Prefix}}static/favicon.svg" sizes="any" type="image/svg+xml">
  <meta name="viewport" content="width=device-width, initial-scale=1.0, minimal-ui">
  <meta name="robots" content="noindex,nofollow">
  <title>Account recovery - Linkding</title>
  <link href="{{.Prefix}}static/theme-dark.css?v={{assetVersion}}" rel="stylesheet" type="text/css" media="(prefers-color-scheme: dark)">
  <link href="{{.Prefix}}static/theme-light.css?v={{assetVersion}}" rel="stylesheet" type="text/css" media="(prefers-color-scheme: light)">
  <meta name="turbo-cache-control" content="no-cache">
  <meta name="turbo-prefetch" content="false">
</head>
<body>
  <header class="container">
    <div class="d-flex justify-between">
      <a href="{{.Prefix}}" class="app-link d-flex align-center">
        <img class="app-logo" src="{{.Prefix}}static/logo.png" alt="Application logo"><span class="app-name">LINKDING</span>
      </a>
      <nav><a href="{{.Prefix}}login/" class="btn btn-link">Login</a></nav>
    </div>
  </header>
  <div class="content container">
    <main class="auth-page" aria-labelledby="main-heading">
      <div class="section-header"><h1 id="main-heading">Account recovery</h1></div>
      {{if .Invalid}}
      <p class="form-input-hint is-error">This recovery link is invalid, expired, or already used. Run <code>linkding passkey_recovery</code> on the server to get a new one.</p>
      {{else}}
      <p>This single-use link signs you in so that you can add a new passkey in the settings.</p>
      <form method="post" action="{{.Prefix}}passkeys/recover">
        <input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}">
        <input type="hidden" name="token" value="{{.Token}}">
        <input type="submit" value="Sign in" class="btn btn-primary width-100 mt-4">
      </form>
      {{end}}
    </main>
  </div>
</body>
</html>`
