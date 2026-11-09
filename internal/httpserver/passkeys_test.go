package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
	"github.com/juev/linkding/internal/store"
)

const passkeyTestOrigin = "http://example.com"

// softAuthenticator is a minimal platform authenticator: one P-256 key with
// "none" attestation, enough to drive the real WebAuthn verification.
type softAuthenticator struct {
	t            *testing.T
	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
	signCount    uint32
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softAuthenticator{t: t, key: key, credentialID: id}
}

func b64url(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func (a *softAuthenticator) authenticatorData(flags byte, attested bool) []byte {
	rpHash := sha256.Sum256([]byte("example.com"))
	data := append([]byte{}, rpHash[:]...)
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, a.signCount)
	if attested {
		x := make([]byte, 32)
		y := make([]byte, 32)
		a.key.PublicKey.X.FillBytes(x) //nolint:staticcheck // the COSE key needs raw coordinates
		a.key.PublicKey.Y.FillBytes(y) //nolint:staticcheck // the COSE key needs raw coordinates
		coseKey, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
			Curve:         int64(webauthncose.P256), XCoord: x, YCoord: y,
		})
		if err != nil {
			a.t.Fatal(err)
		}
		data = append(data, make([]byte, 16)...) // AAGUID
		data = binary.BigEndian.AppendUint16(data, uint16(len(a.credentialID)))
		data = append(data, a.credentialID...)
		data = append(data, coseKey...)
	}
	return data
}

func clientData(t *testing.T, kind, challenge, origin string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// create answers navigator.credentials.create for the given options.
func (a *softAuthenticator) create(options map[string]any) map[string]any {
	publicKey := options["publicKey"].(map[string]any)
	user := publicKey["user"].(map[string]any)
	handle, err := base64.RawURLEncoding.DecodeString(user["id"].(string))
	if err != nil {
		a.t.Fatal(err)
	}
	a.userHandle = handle
	attestation, err := webauthncbor.Marshal(map[string]any{
		"fmt": "none", "attStmt": map[string]any{}, "authData": a.authenticatorData(0x45, true),
	})
	if err != nil {
		a.t.Fatal(err)
	}
	return map[string]any{
		"id": b64url(a.credentialID), "rawId": b64url(a.credentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64url(clientData(a.t, "webauthn.create", publicKey["challenge"].(string), passkeyTestOrigin)),
			"attestationObject": b64url(attestation),
			"transports":        []string{"internal"},
		},
	}
}

// get answers navigator.credentials.get for the given options.
func (a *softAuthenticator) get(options map[string]any, origin string) map[string]any {
	publicKey := options["publicKey"].(map[string]any)
	a.signCount++
	authData := a.authenticatorData(0x05, false)
	client := clientData(a.t, "webauthn.get", publicKey["challenge"].(string), origin)
	clientHash := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return map[string]any{
		"id": b64url(a.credentialID), "rawId": b64url(a.credentialID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": b64url(client), "authenticatorData": b64url(authData),
			"signature": b64url(signature), "userHandle": b64url(a.userHandle),
		},
	}
}

// testBrowser keeps cookies between requests like a browser on one origin.
type testBrowser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newTestBrowser(t *testing.T, handler http.Handler) *testBrowser {
	return &testBrowser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
}

func (b *testBrowser) do(req *http.Request) *httptest.ResponseRecorder {
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, req)
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	return response
}

func (b *testBrowser) get(path string) *httptest.ResponseRecorder {
	return b.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (b *testBrowser) csrfToken() string {
	b.t.Helper()
	cookie, ok := b.cookies[auth.CSRFCookieName]
	if !ok {
		// Every HTML page sets this cookie; tests skip straight to it.
		secret, err := auth.NewCSRFSecret()
		if err != nil {
			b.t.Fatal(err)
		}
		cookie = &http.Cookie{Name: auth.CSRFCookieName, Value: secret}
		b.cookies[auth.CSRFCookieName] = cookie
	}
	token, err := auth.MaskCSRF(cookie.Value)
	if err != nil {
		b.t.Fatal(err)
	}
	return token
}

func (b *testBrowser) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	form.Set("csrfmiddlewaretoken", b.csrfToken())
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", passkeyTestOrigin)
	return b.do(req)
}

func (b *testBrowser) postJSON(path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	b.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRFToken", b.csrfToken())
	req.Header.Set("Origin", passkeyTestOrigin)
	response := b.do(req)
	var decoded map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	return response, decoded
}

func (b *testBrowser) hasSession() bool {
	_, ok := b.cookies[auth.SessionCookieName]
	return ok
}

type passkeyFixture struct {
	db      *sql.DB
	cfg     config.Config
	handler http.Handler
	users   *auth.Repository
	user    auth.User
	token   string
}

func newPasskeyFixture(t *testing.T, cfg config.Config) passkeyFixture {
	t.Helper()
	ctx := context.Background()
	cfg.DBEngine = "sqlite"
	cfg.DataDir = t.TempDir()
	cfg.SessionCookieAge = 3600
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "correct-password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := settings.CreateAPIToken(ctx, db, "sqlite", user.ID, "Extension")
	if err != nil {
		t.Fatal(err)
	}
	return passkeyFixture{db: db, cfg: cfg, handler: New(db, cfg, t.TempDir()), users: users, user: user, token: token}
}

// signInWithSession gives the browser a session without the password form,
// which stays available even when password sign-in is disabled.
func (f passkeyFixture) signInWithSession(t *testing.T, b *testBrowser) {
	t.Helper()
	key, err := f.users.CreateSession(context.Background(), f.user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b.cookies[auth.SessionCookieName] = &http.Cookie{Name: auth.SessionCookieName, Value: key}
}

func registerSoftPasskey(t *testing.T, b *testBrowser, authenticator *softAuthenticator, name string) {
	t.Helper()
	begin, options := b.postJSON("/settings/passkeys/register/begin", map[string]any{})
	if begin.Code != http.StatusOK {
		t.Fatalf("register begin: %d %s", begin.Code, begin.Body.String())
	}
	publicKey := options["publicKey"].(map[string]any)
	selection := publicKey["authenticatorSelection"].(map[string]any)
	if publicKey["rp"].(map[string]any)["id"] != "example.com" || selection["residentKey"] != "required" {
		t.Fatalf("registration options: %+v", publicKey)
	}
	finish, result := b.postJSON("/settings/passkeys/register/finish", map[string]any{"name": name, "credential": authenticator.create(options)})
	if finish.Code != http.StatusOK || result["redirect"] != "/settings/general" {
		t.Fatalf("register finish: %d %s", finish.Code, finish.Body.String())
	}
}

func signInWithSoftPasskey(t *testing.T, b *testBrowser, authenticator *softAuthenticator, next string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	begin, options := b.postJSON("/passkeys/login/begin", map[string]any{})
	if begin.Code != http.StatusOK {
		t.Fatalf("login begin: %d %s", begin.Code, begin.Body.String())
	}
	if allowed, ok := options["publicKey"].(map[string]any)["allowCredentials"]; ok && allowed != nil {
		t.Fatalf("discoverable login sent an allow list: %v", allowed)
	}
	path := "/passkeys/login/finish"
	if next != "" {
		path += "?next=" + url.QueryEscape(next)
	}
	return b.postJSON(path, authenticator.get(options, passkeyTestOrigin))
}

func TestPasskeyRegisterSignOutAndSignIn(t *testing.T) {
	f := newPasskeyFixture(t, config.Config{})
	b := newTestBrowser(t, f.handler)
	f.signInWithSession(t, b)
	authenticator := newSoftAuthenticator(t)
	registerSoftPasskey(t, b, authenticator, "Laptop")

	page := b.get("/settings/general")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Passkey &#34;Laptop&#34; added.") || !strings.Contains(page.Body.String(), "<td>Laptop</td>") {
		t.Fatalf("settings after registration: %d %s", page.Code, page.Body.String())
	}

	// Registering the same authenticator again is excluded and refused.
	begin, options := b.postJSON("/settings/passkeys/register/begin", map[string]any{})
	excluded := options["publicKey"].(map[string]any)["excludeCredentials"].([]any)
	if begin.Code != http.StatusOK || len(excluded) != 1 || excluded[0].(map[string]any)["id"] != b64url(authenticator.credentialID) {
		t.Fatalf("exclusions: %d %+v", begin.Code, options)
	}
	if again, _ := b.postJSON("/settings/passkeys/register/finish", map[string]any{"name": "Again", "credential": authenticator.create(options)}); again.Code != http.StatusBadRequest {
		t.Fatalf("duplicate registration: %d %s", again.Code, again.Body.String())
	}

	if logout := b.postForm("/logout/", url.Values{}); logout.Code != http.StatusFound || b.hasSession() {
		t.Fatalf("logout: %d session=%v", logout.Code, b.hasSession())
	}
	if page := b.get("/settings/general"); page.Code != http.StatusFound {
		t.Fatalf("settings after logout: %d", page.Code)
	}

	login := newTestBrowser(t, f.handler)
	loginPage := login.get("/login/")
	if !strings.Contains(loginPage.Body.String(), "data-passkey-login") {
		t.Fatalf("login page has no passkey button: %s", loginPage.Body.String())
	}
	finish, result := signInWithSoftPasskey(t, login, authenticator, "/bookmarks?q=%23go")
	if finish.Code != http.StatusOK || result["redirect"] != "/bookmarks?q=%23go" || !login.hasSession() {
		t.Fatalf("passkey sign-in: %d %s", finish.Code, finish.Body.String())
	}
	if page := login.get("/settings/general"); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<td>Laptop</td>") || strings.Contains(page.Body.String(), "<td>Never</td>") {
		t.Fatalf("settings after passkey sign-in: %d", page.Code)
	}
	api := login.get("/api/bookmarks/")
	if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), `"results"`) {
		t.Fatalf("session API call after passkey sign-in: %d %s", api.Code, api.Body.String())
	}
	var lastLogin sql.NullTime
	if err := f.db.QueryRow(`SELECT last_login FROM auth_user WHERE id = ?`, f.user.ID).Scan(&lastLogin); err != nil || !lastLogin.Valid {
		t.Fatalf("passkey sign-in did not record last_login: %v %v", lastLogin, err)
	}
	passkeys, err := f.users.ListPasskeys(context.Background(), f.user.ID)
	if err != nil || len(passkeys) != 1 || passkeys[0].Credential.Authenticator.SignCount != 1 {
		t.Fatalf("stored sign count: %+v err=%v", passkeys, err)
	}
}

func TestPasskeySignInRejectsReplayForeignOriginAndRegressedCounter(t *testing.T) {
	f := newPasskeyFixture(t, config.Config{})
	b := newTestBrowser(t, f.handler)
	f.signInWithSession(t, b)
	authenticator := newSoftAuthenticator(t)
	registerSoftPasskey(t, b, authenticator, "Laptop")

	// Finishing without a begin request has no challenge to verify against.
	fresh := newTestBrowser(t, f.handler)
	if response, _ := fresh.postJSON("/passkeys/login/finish", map[string]any{}); response.Code != http.StatusBadRequest || fresh.hasSession() {
		t.Fatalf("finish without challenge: %d", response.Code)
	}

	attacker := newTestBrowser(t, f.handler)
	_, options := attacker.postJSON("/passkeys/login/begin", map[string]any{})
	if response, _ := attacker.postJSON("/passkeys/login/finish", authenticator.get(options, "https://evil.example")); response.Code != http.StatusUnauthorized || attacker.hasSession() {
		t.Fatalf("foreign origin: %d %s", response.Code, response.Body.String())
	}

	victim := newTestBrowser(t, f.handler)
	_, options = victim.postJSON("/passkeys/login/begin", map[string]any{})
	assertion := authenticator.get(options, passkeyTestOrigin)
	challengeCookie := victim.cookies[passkeyChallengeCookie]
	if response, _ := victim.postJSON("/passkeys/login/finish", assertion); response.Code != http.StatusOK {
		t.Fatalf("first sign-in: %d %s", response.Code, response.Body.String())
	}
	replay := newTestBrowser(t, f.handler)
	replay.cookies[passkeyChallengeCookie] = challengeCookie
	if response, _ := replay.postJSON("/passkeys/login/finish", assertion); response.Code != http.StatusBadRequest || replay.hasSession() {
		t.Fatalf("replayed assertion: %d %s", response.Code, response.Body.String())
	}

	clone := newTestBrowser(t, f.handler)
	authenticator.signCount = 0 // get() increments it to 1, below the stored counter.
	if response, _ := signInWithSoftPasskey(t, clone, authenticator, ""); response.Code != http.StatusUnauthorized || clone.hasSession() {
		t.Fatalf("regressed sign counter: %d %s", response.Code, response.Body.String())
	}

	if _, err := f.db.Exec(`UPDATE auth_user SET is_active = 0 WHERE id = ?`, f.user.ID); err != nil {
		t.Fatal(err)
	}
	inactive := newTestBrowser(t, f.handler)
	authenticator.signCount = 10
	if response, _ := signInWithSoftPasskey(t, inactive, authenticator, ""); response.Code != http.StatusUnauthorized || inactive.hasSession() {
		t.Fatalf("inactive user signed in: %d %s", response.Code, response.Body.String())
	}
}

func TestPasswordLoginDisabled(t *testing.T) {
	f := newPasskeyFixture(t, config.Config{DisablePasswordLogin: true})
	b := newTestBrowser(t, f.handler)

	page := b.get("/login/")
	body := page.Body.String()
	if page.Code != http.StatusOK || strings.Contains(body, `name="password"`) || !strings.Contains(body, "data-passkey-login") || !strings.Contains(body, "Password sign-in is disabled") {
		t.Fatalf("login page: %d %s", page.Code, body)
	}
	countSessions := func() int {
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM django_session`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := countSessions()
	login := b.postForm("/login/", url.Values{"username": {"alice"}, "password": {"correct-password"}})
	if login.Code != http.StatusForbidden || b.hasSession() || strings.Contains(login.Body.String(), `name="password"`) {
		t.Fatalf("password login: %d session=%v", login.Code, b.hasSession())
	}
	admin := b.postForm("/admin/login/", url.Values{"username": {"alice"}, "password": {"correct-password"}})
	if admin.Code != http.StatusForbidden || b.hasSession() {
		t.Fatalf("admin password login: %d session=%v", admin.Code, b.hasSession())
	}
	if after := countSessions(); after != before {
		t.Fatalf("password attempts created %d sessions", after-before)
	}
	redirect := b.get("/admin/login/?next=/admin/bookmarks/bookmark/")
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "/login/?next=%2Fadmin%2Fbookmarks%2Fbookmark%2F" {
		t.Fatalf("admin login redirect: %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	if redirect := b.get("/admin/login/"); redirect.Header().Get("Location") != "/login/?next=%2Fadmin%2F" {
		t.Fatalf("admin login default redirect: %q", redirect.Header().Get("Location"))
	}

	// A passkey session opens the admin for a staff user.
	f.signInWithSession(t, b)
	authenticator := newSoftAuthenticator(t)
	registerSoftPasskey(t, b, authenticator, "Laptop")
	b.postForm("/logout/", url.Values{})
	passkeyBrowser := newTestBrowser(t, f.handler)
	finish, result := signInWithSoftPasskey(t, passkeyBrowser, authenticator, "/admin/")
	if finish.Code != http.StatusOK || result["redirect"] != "/admin/" {
		t.Fatalf("passkey sign-in: %d %s", finish.Code, finish.Body.String())
	}
	if adminPage := passkeyBrowser.get("/admin/"); adminPage.Code != http.StatusOK || !strings.Contains(adminPage.Body.String(), "Site administration") {
		t.Fatalf("admin with passkey session: %d", adminPage.Code)
	}
	if adminLogin := passkeyBrowser.get("/admin/login/"); adminLogin.Code != http.StatusFound || adminLogin.Header().Get("Location") != "/admin/" {
		t.Fatalf("admin login for signed-in staff: %d %q", adminLogin.Code, adminLogin.Header().Get("Location"))
	}

	// The last passkey cannot be deleted while passwords are off.
	passkeys, err := f.users.ListPasskeys(context.Background(), f.user.ID)
	if err != nil || len(passkeys) != 1 {
		t.Fatalf("passkeys: %+v err=%v", passkeys, err)
	}
	id := url.Values{"passkey_id": {strconv.FormatInt(passkeys[0].ID, 10)}}
	if response := passkeyBrowser.postForm("/settings/passkeys/delete", id); response.Code != http.StatusFound {
		t.Fatalf("delete last passkey: %d", response.Code)
	}
	if page := passkeyBrowser.get("/settings/general"); !strings.Contains(page.Body.String(), "You cannot delete your last passkey") || !strings.Contains(page.Body.String(), "<td>Laptop</td>") {
		t.Fatalf("settings after refused deletion: %s", page.Body.String())
	}
	second := newSoftAuthenticator(t)
	registerSoftPasskey(t, passkeyBrowser, second, "Phone")
	if response := passkeyBrowser.postForm("/settings/passkeys/delete", id); response.Code != http.StatusFound {
		t.Fatalf("delete one of two passkeys: %d", response.Code)
	}
	if page := passkeyBrowser.get("/settings/general"); !strings.Contains(page.Body.String(), "Passkey &#34;Laptop&#34; deleted.") || strings.Contains(page.Body.String(), "<td>Laptop</td>") {
		t.Fatalf("settings after deletion: %s", page.Body.String())
	}
}

func TestPasskeyDeleteWithPasswordLoginEnabled(t *testing.T) {
	f := newPasskeyFixture(t, config.Config{})
	b := newTestBrowser(t, f.handler)
	f.signInWithSession(t, b)
	registerSoftPasskey(t, b, newSoftAuthenticator(t), "Laptop")
	passkeys, err := f.users.ListPasskeys(context.Background(), f.user.ID)
	if err != nil || len(passkeys) != 1 {
		t.Fatalf("passkeys: %+v err=%v", passkeys, err)
	}
	other := newTestBrowser(t, f.handler)
	bob, err := f.users.CreateUser(context.Background(), auth.NewUser{Username: "bob", Password: "bob-password"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.users.CreateSession(context.Background(), bob.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	other.cookies[auth.SessionCookieName] = &http.Cookie{Name: auth.SessionCookieName, Value: key}
	id := url.Values{"passkey_id": {strconv.FormatInt(passkeys[0].ID, 10)}}
	if response := other.postForm("/settings/passkeys/delete", id); response.Code != http.StatusNotFound {
		t.Fatalf("foreign passkey deletion: %d", response.Code)
	}
	if response := b.postForm("/settings/passkeys/delete", id); response.Code != http.StatusFound {
		t.Fatalf("delete only passkey: %d", response.Code)
	}
	if remaining, err := f.users.ListPasskeys(context.Background(), f.user.ID); err != nil || len(remaining) != 0 {
		t.Fatalf("passkeys after deletion: %+v err=%v", remaining, err)
	}
}

func TestPasskeyEndpointsRequireSessionAndCSRF(t *testing.T) {
	f := newPasskeyFixture(t, config.Config{})
	anonymous := newTestBrowser(t, f.handler)
	if response, _ := anonymous.postJSON("/settings/passkeys/register/begin", map[string]any{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous registration: %d", response.Code)
	}
	signedIn := newTestBrowser(t, f.handler)
	f.signInWithSession(t, signedIn)
	signedIn.get("/login/")
	req := httptest.NewRequest(http.MethodPost, "/settings/passkeys/register/begin", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if response := signedIn.do(req); response.Code != http.StatusForbidden {
		t.Fatalf("registration without CSRF token: %d", response.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/passkeys/login/begin", strings.NewReader("{}"))
	if response := anonymous.do(req); response.Code != http.StatusForbidden {
		t.Fatalf("login begin without CSRF token: %d", response.Code)
	}
	if response := signedIn.get("/passkeys/login/begin"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("login begin with GET: %d", response.Code)
	}
}

func TestPasskeyRecoveryLink(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		f := newPasskeyFixture(t, config.Config{DisablePasswordLogin: disabled})
		token, err := f.users.CreateRecoveryToken(context.Background(), f.user.ID, 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		b := newTestBrowser(t, f.handler)
		page := b.get("/passkeys/recover?token=" + url.QueryEscape(token))
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `value="`+token+`"`) || b.hasSession() || page.Header().Get("Referrer-Policy") != "same-origin" {
			t.Fatalf("recovery GET: %d session=%v", page.Code, b.hasSession())
		}
		// GET did not consume the token: link previews must not burn it.
		signIn := b.postForm("/passkeys/recover", url.Values{"token": {token}})
		if signIn.Code != http.StatusFound || signIn.Header().Get("Location") != "/settings/general" || !b.hasSession() {
			t.Fatalf("recovery POST (disabled=%v): %d %q", disabled, signIn.Code, signIn.Header().Get("Location"))
		}
		if settingsPage := b.get("/settings/general"); !strings.Contains(settingsPage.Body.String(), "Signed in with a recovery link") {
			t.Fatalf("settings after recovery: %s", settingsPage.Body.String())
		}
		again := newTestBrowser(t, f.handler)
		reuse := again.postForm("/passkeys/recover", url.Values{"token": {token}})
		if reuse.Code != http.StatusBadRequest || again.hasSession() || !strings.Contains(reuse.Body.String(), "invalid, expired, or already used") {
			t.Fatalf("reused recovery token: %d session=%v", reuse.Code, again.hasSession())
		}
		expired, err := f.users.CreateRecoveryToken(context.Background(), f.user.ID, 15*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE linkding_recovery_token SET expires_at = ?`, time.Now().UTC().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		late := newTestBrowser(t, f.handler)
		if response := late.postForm("/passkeys/recover", url.Values{"token": {expired}}); response.Code != http.StatusBadRequest || late.hasSession() {
			t.Fatalf("expired recovery token: %d session=%v", response.Code, late.hasSession())
		}
		if response := late.get("/passkeys/recover"); response.Code != http.StatusOK || strings.Contains(response.Body.String(), `name="token"`) {
			t.Fatalf("recovery page without token: %d", response.Code)
		}
	}
}

// TestAPIClientsUnaffectedByPasswordLoginSetting compares token-authenticated
// API responses with password sign-in enabled and disabled.
func TestAPIClientsUnaffectedByPasswordLoginSetting(t *testing.T) {
	type observed struct {
		status int
		body   string
	}
	run := func(disabled bool) map[string]observed {
		f := newPasskeyFixture(t, config.Config{DisablePasswordLogin: disabled})
		feedKey, err := settings.EnsureFeedToken(context.Background(), f.db, "sqlite", f.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		results := map[string]observed{}
		for _, scheme := range []string{"Token", "Bearer"} {
			for _, path := range []string{"/api/", "/api/bookmarks/", "/api/bookmarks/archived/", "/api/tags/", "/api/bundles/", "/api/user/profile/"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", scheme+" "+f.token)
				response := httptest.NewRecorder()
				f.handler.ServeHTTP(response, req)
				results[scheme+" "+path] = observed{response.Code, response.Body.String()}
			}
		}
		create := httptest.NewRequest(http.MethodPost, "/api/bookmarks/", strings.NewReader(`{"url":"https://example.org/","title":"Example","tag_names":["go"]}`))
		create.Header.Set("Authorization", "Token "+f.token)
		create.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, create)
		var created map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &created)
		results["create"] = observed{response.Code, ""}
		if created["url"] != "https://example.org/" {
			t.Fatalf("create bookmark (disabled=%v): %d %s", disabled, response.Code, response.Body.String())
		}
		invalid := httptest.NewRequest(http.MethodGet, "/api/bookmarks/", nil)
		invalid.Header.Set("Authorization", "Token wrong")
		response = httptest.NewRecorder()
		f.handler.ServeHTTP(response, invalid)
		results["invalid token"] = observed{response.Code, response.Body.String()}
		feed := httptest.NewRequest(http.MethodGet, "/feeds/"+feedKey+"/all", nil)
		response = httptest.NewRecorder()
		f.handler.ServeHTTP(response, feed)
		body, _ := io.ReadAll(response.Result().Body)
		results["feed"] = observed{response.Code, ""}
		if response.Code != http.StatusOK || !strings.Contains(string(body), "https://example.org/") {
			t.Fatalf("feed (disabled=%v): %d %s", disabled, response.Code, body)
		}
		return results
	}
	enabled := run(false)
	disabled := run(true)
	for key, want := range enabled {
		got := disabled[key]
		if key != "create" && key != "feed" && key != "invalid token" && want.status != http.StatusOK {
			t.Fatalf("%s with passwords enabled: %d %s", key, want.status, want.body)
		}
		if got.status != want.status || normalizeAPIDates(got.body) != normalizeAPIDates(want.body) {
			t.Fatalf("%s differs with password sign-in disabled:\nenabled:  %d %s\ndisabled: %d %s", key, want.status, want.body, got.status, got.body)
		}
	}
}

// normalizeAPIDates removes timestamps, which differ between the two runs.
func normalizeAPIDates(body string) string {
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return body
	}
	var strip func(any) any
	strip = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			for key, inner := range typed {
				if strings.HasPrefix(key, "date_") {
					typed[key] = ""
				} else {
					typed[key] = strip(inner)
				}
			}
		case []any:
			for i, inner := range typed {
				typed[i] = strip(inner)
			}
		}
		return value
	}
	normalized, _ := json.Marshal(strip(decoded))
	return string(normalized)
}

func TestPasskeyEndToEndPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DBEngine: "postgres", DataDir: t.TempDir(), SessionCookieAge: 3600, DisablePasswordLogin: true}
	users := auth.NewRepository(db, "postgres")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "passkey_pg_" + strconv.FormatInt(time.Now().UnixNano(), 10), Password: "pg-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM bookmarks_userprofile WHERE user_id = $1`, `DELETE FROM auth_user WHERE id = $1`} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
	handler := New(db, cfg, t.TempDir())
	b := newTestBrowser(t, handler)
	key, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b.cookies[auth.SessionCookieName] = &http.Cookie{Name: auth.SessionCookieName, Value: key}
	authenticator := newSoftAuthenticator(t)
	registerSoftPasskey(t, b, authenticator, "Laptop")
	fresh := newTestBrowser(t, handler)
	if response, _ := signInWithSoftPasskey(t, fresh, authenticator, ""); response.Code != http.StatusOK || !fresh.hasSession() {
		t.Fatalf("PostgreSQL passkey sign-in: %d %s", response.Code, response.Body.String())
	}
	passkeys, err := users.ListPasskeys(ctx, user.ID)
	if err != nil || len(passkeys) != 1 || !passkeys[0].LastUsedAt.Valid {
		t.Fatalf("PostgreSQL passkeys: %+v err=%v", passkeys, err)
	}
	if _, err := users.DeletePasskey(ctx, user.ID, passkeys[0].ID, true); err == nil {
		t.Fatal("PostgreSQL deleted the last passkey with keepLast")
	}
}
