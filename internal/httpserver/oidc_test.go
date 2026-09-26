package httpserver

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestOIDCRedirectURLBehindTrustedProxy(t *testing.T) {
	cases := []struct {
		name    string
		trusted bool
		proto   string
		tls     bool
		want    string
	}{
		{"ignore untrusted headers", false, "https", false, "http://internal.test/oidc/callback/"},
		{"use trusted HTTPS", true, "https", false, "https://links.test/oidc/callback/"},
		{"use trusted HTTP", true, "http", false, "http://links.test/oidc/callback/"},
		{"ignore invalid scheme", true, "javascript", false, "http://links.test/oidc/callback/"},
		{"keep direct TLS", true, "http", true, "https://links.test/oidc/callback/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://internal.test/oidc/authenticate/", nil)
			r.Header.Set("X-Forwarded-Host", "links.test")
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got := oidcRedirectURL(r, config.Config{UseXForwardedHost: tc.trusted})
			if got != tc.want {
				t.Fatalf("callback URL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOIDCMethodAndCSRFResponses(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), EnableOIDC: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	cases := []struct {
		method, path string
		status       int
		allow        string
		contentType  string
		empty        bool
	}{
		{"POST", "/oidc/authenticate/", 403, "", "text/html; charset=utf-8", false},
		{"POST", "/oidc/callback/", 403, "", "text/html; charset=utf-8", false},
		{"POST", "/oidc/logout/", 403, "", "text/html; charset=utf-8", false},
		{"HEAD", "/oidc/authenticate/", 405, "GET", "text/html; charset=utf-8", true},
		{"OPTIONS", "/oidc/callback/", 405, "GET", "text/html; charset=utf-8", true},
		{"GET", "/oidc/logout/", 405, "POST", "text/html; charset=utf-8", true},
		{"HEAD", "/oidc/logout/", 405, "GET, POST", "text/html; charset=utf-8", true},
		{"OPTIONS", "/oidc/logout/", 405, "GET, POST", "text/html; charset=utf-8", true},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://linkding.test"+tc.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || response.Header().Get("Allow") != tc.allow || response.Header().Get("Content-Type") != tc.contentType || response.Header().Get("Content-Language") != "en" {
				t.Fatalf("response: %d Allow=%q Content-Type=%q body=%q", response.Code, response.Header().Get("Allow"), response.Header().Get("Content-Type"), response.Body.String())
			}
			if tc.empty && response.Body.Len() != 0 {
				t.Fatalf("expected empty body, got %q", response.Body.String())
			}
			if !tc.empty && response.Body.Len() == 0 {
				t.Fatal("expected CSRF failure body")
			}
		})
	}
}

func TestOIDCCodeFlowPKCEClaimsAndReplay(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, nonce, challenge string
	op := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			verifier := r.Form.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			if r.Form.Get("code") != "valid-code" || r.Form.Get("client_id") != "client" || r.Form.Get("client_secret") != "secret" || verifier == "" || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
				t.Errorf("bad token exchange: %v", r.Form)
				w.WriteHeader(400)
				return
			}
			idToken := signedOIDCTestToken(t, key, map[string]any{"iss": issuer, "aud": "client", "sub": "subject", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access", "token_type": "Bearer", "id_token": idToken})
		case "/jwks":
			e := big.NewInt(int64(key.PublicKey.E)).Bytes()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("userinfo auth: %q", r.Header.Get("Authorization"))
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"email":"person@example.com","preferred_username":"ＮｏｒｍａｌｉｚｅｄＵｓｅｒ"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer op.Close()
	issuer = op.URL
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true, EnableOIDC: true, OIDC: config.OIDCConfig{AuthorizationEndpoint: op.URL + "/authorize", TokenEndpoint: op.URL + "/token", UserEndpoint: op.URL + "/userinfo", JWKSEndpoint: op.URL + "/jwks", ClientID: "client", ClientSecret: "secret", SignAlgorithm: "RS256", Scopes: "openid email profile", UsePKCE: true, VerifySSL: true, UsernameClaim: "preferred_username"}}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	begin := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/authenticate/?next=%2Fbookmarks", nil)
	beginResponse := httptest.NewRecorder()
	handler.ServeHTTP(beginResponse, begin)
	if beginResponse.Code != 302 {
		t.Fatalf("begin: %d %q", beginResponse.Code, beginResponse.Body.String())
	}
	redirect, err := url.Parse(beginResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Path != "/authorize" || redirect.Query().Get("redirect_uri") != "http://linkding.test/oidc/callback/" || redirect.Query().Get("client_id") != "client" || redirect.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization request: %s", redirect)
	}
	state := redirect.Query().Get("state")
	nonce = redirect.Query().Get("nonce")
	challenge = redirect.Query().Get("code_challenge")
	if state == "" || nonce == "" || challenge == "" {
		t.Fatalf("missing OIDC flow values: %s", redirect)
	}
	var stateCookie *http.Cookie
	for _, cookie := range beginResponse.Result().Cookies() {
		if cookie.Name == oidcStateCookie {
			stateCookie = cookie
		}
	}
	if stateCookie == nil {
		t.Fatal("missing state cookie")
	}
	callback := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/callback/?state="+url.QueryEscape(state)+"&code=valid-code", nil)
	callback.AddCookie(stateCookie)
	loginStarted := time.Now().UTC()
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != 302 || callbackResponse.Header().Get("Location") != "/bookmarks" {
		t.Fatalf("callback: %d %q", callbackResponse.Code, callbackResponse.Header().Get("Location"))
	}
	var session *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			session = cookie
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("OIDC callback did not create session")
	}
	var username, email, password string
	if err := db.QueryRowContext(ctx, `SELECT username,email,password FROM auth_user WHERE email=?`, "person@example.com").Scan(&username, &email, &password); err != nil || username != "NormalizedUser" || password != "!" {
		t.Fatalf("OIDC user: %q %q %q %v", username, email, password, err)
	}
	var lastLogin sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT last_login FROM auth_user WHERE email=?`, "person@example.com").Scan(&lastLogin); err != nil || !lastLogin.Valid || lastLogin.Time.Before(loginStarted.Add(-time.Second)) || lastLogin.Time.After(time.Now().Add(time.Second)) {
		t.Fatalf("OIDC login did not update last_login: %v %v", lastLogin, err)
	}
	bookmarksRequest := httptest.NewRequest(http.MethodGet, "http://linkding.test/bookmarks", nil)
	bookmarksRequest.AddCookie(session)
	bookmarksResponse := httptest.NewRecorder()
	handler.ServeHTTP(bookmarksResponse, bookmarksRequest)
	if bookmarksResponse.Code != 200 {
		t.Fatalf("authenticated bookmark page: %d", bookmarksResponse.Code)
	}
	replay := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/callback/?state="+url.QueryEscape(state)+"&code=valid-code", nil)
	replay.AddCookie(stateCookie)
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != 302 || replayResponse.Header().Get("Location") != "/" {
		t.Fatalf("replayed callback: %d %q", replayResponse.Code, replayResponse.Header().Get("Location"))
	}
}

func TestOIDCConcurrentBrowserFlows(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), EnableOIDC: true, OIDC: config.OIDCConfig{AuthorizationEndpoint: "https://issuer.example/authorize", ClientID: "client", Scopes: "openid"}}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	firstRequest := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/authenticate/?next=%2Fbookmarks", nil)
	firstResponse := httptest.NewRecorder()
	beginOIDC(firstResponse, firstRequest, cfg, db)
	firstURL, _ := url.Parse(firstResponse.Header().Get("Location"))
	firstCookie := oidcTestStateCookie(t, firstResponse)

	secondRequest := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/authenticate/?next=%2Ftags", nil)
	secondRequest.AddCookie(firstCookie)
	secondResponse := httptest.NewRecorder()
	beginOIDC(secondResponse, secondRequest, cfg, db)
	secondURL, _ := url.Parse(secondResponse.Header().Get("Location"))
	secondCookie := oidcTestStateCookie(t, secondResponse)
	if firstCookie.Value != secondCookie.Value || firstURL.Query().Get("state") == secondURL.Query().Get("state") {
		t.Fatal("browser state cookie was replaced or OIDC states were reused")
	}
	badRequest := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/callback/?state=unknown", nil)
	badRequest.AddCookie(secondCookie)
	if _, err := consumeOIDCState(badRequest, cfg, db); err == nil {
		t.Fatal("unknown state was accepted")
	}
	for _, expected := range []struct{ state, next string }{{firstURL.Query().Get("state"), "/bookmarks"}, {secondURL.Query().Get("state"), "/tags"}} {
		callback := httptest.NewRequest(http.MethodGet, "http://linkding.test/oidc/callback/?state="+url.QueryEscape(expected.state), nil)
		callback.AddCookie(secondCookie)
		flow, err := consumeOIDCState(callback, cfg, db)
		if err != nil || flow.State != expected.state || flow.Next != expected.next {
			t.Fatalf("flow %q: %+v %v", expected.state, flow, err)
		}
		if _, err := consumeOIDCState(callback, cfg, db); err == nil {
			t.Fatalf("flow %q was accepted twice", expected.state)
		}
	}
}

func oidcTestStateCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oidcStateCookie {
			return cookie
		}
	}
	t.Fatal("missing OIDC state cookie")
	return nil
}

func signedOIDCTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestOIDCHMACAndSignedUserInfo(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	op := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			e := big.NewInt(int64(key.PublicKey.E)).Bytes()
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test-key", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
		case "/userinfo":
			w.Header().Set("Content-Type", "application/jwt")
			_, _ = w.Write([]byte(signedOIDCTestToken(t, key, map[string]any{"iss": issuer, "email": "jwt@example.com", "exp": time.Now().Add(time.Hour).Unix()})))
		default:
			http.NotFound(w, r)
		}
	}))
	defer op.Close()
	issuer = op.URL
	cfg := config.Config{OIDC: config.OIDCConfig{UserEndpoint: op.URL + "/userinfo", JWKSEndpoint: op.URL + "/jwks", SignAlgorithm: "RS256"}}
	claims, err := fetchOIDCUserInfo(ctx, op.Client(), cfg, "access")
	if err != nil || claims["email"] != "jwt@example.com" {
		t.Fatalf("signed userinfo: %v %v", claims, err)
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"iss": "issuer", "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": "expected"})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(input))
	token := input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	hmacCfg := config.Config{OIDC: config.OIDCConfig{SignAlgorithm: "HS256", ClientID: "client", ClientSecret: "secret"}}
	if err := verifyOIDCIDToken(ctx, op.Client(), hmacCfg, token, "expected"); err != nil {
		t.Fatalf("valid HMAC token: %v", err)
	}
	if err := verifyOIDCIDToken(ctx, op.Client(), hmacCfg, token, "wrong"); err == nil {
		t.Fatal("wrong nonce accepted")
	}
	if err := verifyOIDCIDToken(ctx, op.Client(), hmacCfg, token+"x", "expected"); err == nil {
		t.Fatal("bad signature accepted")
	}
}
