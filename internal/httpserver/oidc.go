package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/text/unicode/norm"
)

const oidcStateCookie = "ld_oidc_state"

type oidcFlowState struct {
	State, Nonce, Verifier, Next string
}

func serveOIDC(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if !cfg.EnableOIDC {
		writeNotFound(w, r)
		return
	}
	switch r.URL.Path {
	case root + "authenticate/":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method not allowed", 405)
			return
		}
		beginOIDC(w, r, cfg, db)
	case root + "callback/":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method not allowed", 405)
			return
		}
		completeOIDC(w, r, cfg, db, users)
	case root + "logout/":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Method not allowed", 405)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			writeCSRFFailure(w, r)
			return
		}
		if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
			if err := users.DeleteSession(r.Context(), cookie.Value); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
		}
		clearProxySession(w, cfg.URLPrefix())
		writeRedirect(w, r, cfg.URLPrefix()+"login/")
	default:
		writeNotFound(w, r)
	}
}

func oidcRedirectURL(r *http.Request, cfg config.Config) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if cfg.UseXForwardedHost {
		if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
			host = forwarded
		}
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: cfg.URLPrefix() + "oidc/callback/"}).String()
}

func oidcOAuthConfig(r *http.Request, cfg config.Config) oauth2.Config {
	return oauth2.Config{ClientID: cfg.OIDC.ClientID, ClientSecret: cfg.OIDC.ClientSecret, RedirectURL: oidcRedirectURL(r, cfg), Scopes: strings.Fields(cfg.OIDC.Scopes), Endpoint: oauth2.Endpoint{AuthURL: cfg.OIDC.AuthorizationEndpoint, TokenURL: cfg.OIDC.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams}}
}

func beginOIDC(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB) {
	if cfg.OIDC.AuthorizationEndpoint == "" || cfg.OIDC.ClientID == "" {
		http.Error(w, "OIDC is not configured", 500)
		return
	}
	state, err := oidcRandom(24)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	nonce, err := oidcRandom(24)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	cookieKey := ""
	if cookie, err := r.Cookie(oidcStateCookie); err == nil {
		cookieKey = cookie.Value
	}
	if cookieKey == "" {
		cookieKey, err = oidcRandom(24)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	flow := oidcFlowState{State: state, Nonce: nonce, Next: safeNext(r.URL.Query().Get("next"), r, "")}
	options := []oauth2.AuthCodeOption{oidc.Nonce(nonce)}
	if cfg.OIDC.UsePKCE {
		flow.Verifier = oauth2.GenerateVerifier()
		options = append(options, oauth2.S256ChallengeOption(flow.Verifier))
	}
	encoded, err := json.Marshal(flow)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	query := `INSERT INTO django_session(session_key,session_data,expire_date) VALUES (` + assetMarker(cfg.DBEngine, 1) + `,` + assetMarker(cfg.DBEngine, 2) + `,` + assetMarker(cfg.DBEngine, 3) + `)`
	if _, err := db.ExecContext(r.Context(), query, oidcStorageKey(cookieKey, state), "oidc1:"+base64.RawStdEncoding.EncodeToString(encoded), time.Now().UTC().Add(10*time.Minute)); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: cookieKey, Path: cfg.URLPrefix() + "oidc/", MaxAge: 600, Expires: time.Now().Add(10 * time.Minute), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	config := oidcOAuthConfig(r, cfg)
	writeRedirect(w, r, config.AuthCodeURL(state, options...))
}

func completeOIDC(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository) {
	flow, err := consumeOIDCState(r, cfg, db)
	if err != nil || r.URL.Query().Get("error") != "" {
		writeRedirect(w, r, "/")
		return
	}
	if r.URL.Query().Get("state") != flow.State {
		http.Error(w, "OIDC callback state not found", 400)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeRedirect(w, r, "/")
		return
	}
	client := oidcHTTPClient(cfg)
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, client)
	config := oidcOAuthConfig(r, cfg)
	options := []oauth2.AuthCodeOption{}
	if flow.Verifier != "" {
		options = append(options, oauth2.VerifierOption(flow.Verifier))
	}
	token, err := config.Exchange(ctx, code, options...)
	if err != nil {
		writeRedirect(w, r, "/")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		writeRedirect(w, r, "/")
		return
	}
	if err := verifyOIDCIDToken(ctx, client, cfg, rawIDToken, flow.Nonce); err != nil {
		writeRedirect(w, r, "/")
		return
	}
	claims, err := fetchOIDCUserInfo(ctx, client, cfg, token.AccessToken)
	if err != nil {
		writeRedirect(w, r, "/")
		return
	}
	if strings.Contains(" "+cfg.OIDC.Scopes+" ", " email ") {
		if _, ok := claims["email"]; !ok {
			writeRedirect(w, r, "/")
			return
		}
	}
	email, _ := claims["email"].(string)
	username, _ := claims[cfg.OIDC.UsernameClaim].(string)
	if username == "" {
		username = email
	}
	username = norm.NFKC.String(username)
	runes := []rune(username)
	if len(runes) > 150 {
		username = string(runes[:150])
	}
	user, err := users.GetOrCreateOIDCUser(r.Context(), email, username)
	if err != nil {
		writeRedirect(w, r, "/")
		return
	}
	age := cfg.SessionCookieAge
	if age <= 0 {
		age = 1_209_600
	}
	key, err := users.CreateSession(r.Context(), user.ID, time.Duration(age)*time.Second)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if err := users.RecordLogin(r.Context(), user.ID); err != nil {
		_ = users.DeleteSession(r.Context(), key)
		http.Error(w, "Server error", 500)
		return
	}
	if old, err := r.Cookie(auth.SessionCookieName); err == nil {
		_ = users.DeleteSession(r.Context(), old.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: key, Path: cfg.URLPrefix(), MaxAge: age, Expires: time.Now().Add(time.Duration(age) * time.Second), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	destination := flow.Next
	if destination == "" {
		destination = cfg.URLPrefix() + "bookmarks"
	}
	writeRedirect(w, r, destination)
}

func consumeOIDCState(r *http.Request, cfg config.Config, db *sql.DB) (oidcFlowState, error) {
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return oidcFlowState{}, err
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		return oidcFlowState{}, errors.New("missing OIDC state")
	}
	query := `DELETE FROM django_session WHERE session_key = ` + assetMarker(cfg.DBEngine, 1) + ` AND expire_date > ` + assetMarker(cfg.DBEngine, 2) + ` AND session_data LIKE 'oidc1:%' RETURNING session_data`
	var stored string
	if err := db.QueryRowContext(r.Context(), query, oidcStorageKey(cookie.Value, state), time.Now().UTC()).Scan(&stored); err != nil {
		return oidcFlowState{}, err
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, "oidc1:"))
	if err != nil {
		return oidcFlowState{}, err
	}
	var flow oidcFlowState
	if err := json.Unmarshal(data, &flow); err != nil {
		return oidcFlowState{}, err
	}
	if flow.State != state || flow.Nonce == "" {
		return oidcFlowState{}, errors.New("empty OIDC state")
	}
	return flow, nil
}

func oidcStorageKey(cookieKey, state string) string {
	mac := hmac.New(sha256.New, []byte(cookieKey))
	_, _ = mac.Write([]byte(state))
	return hex.EncodeToString(mac.Sum(nil)[:20])
}

func oidcRandom(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func oidcHTTPClient(cfg config.Config) *http.Client {
	client := &http.Client{Timeout: 15 * time.Second}
	if !cfg.OIDC.VerifySSL {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client.Transport = transport
	}
	return client
}

func verifyOIDCIDToken(ctx context.Context, client *http.Client, cfg config.Config, raw, nonce string) error {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return errors.New("invalid ID token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return err
	}
	if header.Alg != cfg.OIDC.SignAlgorithm {
		return fmt.Errorf("unexpected ID token algorithm %q", header.Alg)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims struct {
		Issuer   string          `json:"iss"`
		Nonce    string          `json:"nonce"`
		Audience json.RawMessage `json:"aud"`
		Expires  int64           `json:"exp"`
	}
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return err
	}
	if claims.Nonce != nonce || claims.Expires <= time.Now().Unix() {
		return errors.New("invalid OIDC nonce or expiry")
	}
	if !oidcAudienceMatches(claims.Audience, cfg.OIDC.ClientID) {
		return errors.New("invalid OIDC audience")
	}
	if strings.HasPrefix(header.Alg, "HS") {
		return verifyOIDCHMAC(header.Alg, cfg.OIDC.ClientSecret, parts)
	}
	if cfg.OIDC.JWKSEndpoint == "" || claims.Issuer == "" {
		return errors.New("OIDC JWKS endpoint or issuer missing")
	}
	keyContext := oidc.ClientContext(ctx, client)
	keySet := oidc.NewRemoteKeySet(keyContext, cfg.OIDC.JWKSEndpoint)
	verifier := oidc.NewVerifier(claims.Issuer, keySet, &oidc.Config{ClientID: cfg.OIDC.ClientID, SupportedSigningAlgs: []string{cfg.OIDC.SignAlgorithm}})
	verified, err := verifier.Verify(keyContext, raw)
	if err != nil {
		return err
	}
	if verified.Nonce != nonce {
		return errors.New("invalid OIDC nonce")
	}
	return nil
}

func verifyOIDCHMAC(algorithm, secret string, parts []string) error {
	var digest func() hash.Hash
	switch algorithm {
	case "HS256":
		digest = sha256.New
	case "HS384":
		digest = sha512.New384
	case "HS512":
		digest = sha512.New
	default:
		return errors.New("unsupported HMAC algorithm")
	}
	mac := hmac.New(digest, []byte(secret))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	if !hmac.Equal(mac.Sum(nil), signature) {
		return errors.New("invalid JWT signature")
	}
	return nil
}

func oidcAudienceMatches(raw json.RawMessage, clientID string) bool {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, aud := range many {
		if aud == clientID {
			return true
		}
	}
	return false
}

func fetchOIDCUserInfo(ctx context.Context, client *http.Client, cfg config.Config, accessToken string) (map[string]any, error) {
	if cfg.OIDC.UserEndpoint == "" || accessToken == "" {
		return nil, errors.New("OIDC user endpoint or access token missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.OIDC.UserEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC userinfo returned %d", resp.StatusCode)
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/jwt") {
		payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		return verifyOIDCUserInfoJWT(ctx, client, cfg, string(payload))
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func verifyOIDCUserInfoJWT(ctx context.Context, client *http.Client, cfg config.Config, raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid userinfo JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg != cfg.OIDC.SignAlgorithm {
		return nil, errors.New("invalid userinfo JWT algorithm")
	}
	var payload []byte
	if strings.HasPrefix(header.Alg, "HS") {
		if err := verifyOIDCHMAC(header.Alg, cfg.OIDC.ClientSecret, parts); err != nil {
			return nil, err
		}
		payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	} else {
		if cfg.OIDC.JWKSEndpoint == "" {
			return nil, errors.New("OIDC JWKS endpoint missing")
		}
		keyContext := oidc.ClientContext(ctx, client)
		payload, err = oidc.NewRemoteKeySet(keyContext, cfg.OIDC.JWKSEndpoint).VerifySignature(keyContext, raw)
	}
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if expires, ok := claims["exp"].(float64); ok && int64(expires) <= time.Now().Unix() {
		return nil, errors.New("expired userinfo JWT")
	}
	if nonce, ok := claims["nonce"].(string); ok && nonce != "" {
		return nil, errors.New("unexpected userinfo nonce")
	}
	return claims, nil
}
