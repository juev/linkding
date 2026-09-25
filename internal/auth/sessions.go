package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SessionCookieName = "ld_sessionid"
const CSRFCookieName = "ld_csrftoken"

type sessionData struct {
	UserID       int64  `json:"user_id"`
	PasswordHash string `json:"password_hash"`
	APITokenKey  string `json:"api_token_key,omitempty"`
	APITokenName string `json:"api_token_name,omitempty"`
}

// SetAPITokenReveal stores a newly created token for the next integrations
// page view, matching the one-time display in the upstream session.
func (r *Repository) SetAPITokenReveal(ctx context.Context, sessionKey, tokenKey, tokenName string) error {
	return r.updateTokenReveal(ctx, sessionKey, tokenKey, tokenName)
}

// TakeAPITokenReveal consumes the token after the first page view.
func (r *Repository) TakeAPITokenReveal(ctx context.Context, sessionKey string) (string, string, error) {
	var stored string
	query := `SELECT session_data FROM django_session WHERE session_key = ` + r.marker(1) + ` AND expire_date > ` + r.marker(2)
	if err := r.db.QueryRowContext(ctx, query, sessionKey, time.Now().UTC()).Scan(&stored); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(stored, "go1:") {
		return "", "", ErrInvalidCredentials
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, "go1:"))
	if err != nil {
		return "", "", ErrInvalidCredentials
	}
	var session sessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return "", "", ErrInvalidCredentials
	}
	key, name := session.APITokenKey, session.APITokenName
	if key == "" {
		return "", "", nil
	}
	session.APITokenKey, session.APITokenName = "", ""
	updated, err := json.Marshal(session)
	if err != nil {
		return "", "", err
	}
	query = `UPDATE django_session SET session_data = ` + r.marker(1) + ` WHERE session_key = ` + r.marker(2) + ` AND session_data = ` + r.marker(3)
	result, err := r.db.ExecContext(ctx, query, "go1:"+base64.RawStdEncoding.EncodeToString(updated), sessionKey, stored)
	if err != nil {
		return "", "", err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return "", "", err
	}
	if count != 1 {
		return "", "", nil
	}
	return key, name, nil
}

func (r *Repository) updateTokenReveal(ctx context.Context, sessionKey, tokenKey, tokenName string) error {
	var stored string
	query := `SELECT session_data FROM django_session WHERE session_key = ` + r.marker(1) + ` AND expire_date > ` + r.marker(2)
	if err := r.db.QueryRowContext(ctx, query, sessionKey, time.Now().UTC()).Scan(&stored); err != nil {
		return err
	}
	if !strings.HasPrefix(stored, "go1:") {
		return ErrInvalidCredentials
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, "go1:"))
	if err != nil {
		return ErrInvalidCredentials
	}
	var session sessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return ErrInvalidCredentials
	}
	session.APITokenKey, session.APITokenName = tokenKey, tokenName
	updated, err := json.Marshal(session)
	if err != nil {
		return err
	}
	query = `UPDATE django_session SET session_data = ` + r.marker(1) + ` WHERE session_key = ` + r.marker(2) + ` AND session_data = ` + r.marker(3)
	result, err := r.db.ExecContext(ctx, query, "go1:"+base64.RawStdEncoding.EncodeToString(updated), sessionKey, stored)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("session changed while storing API token")
	}
	return nil
}

// CreateSession stores a new Go session in the existing django_session table.
// The go1 prefix makes old Django sessions ineligible, as agreed for migration.
func (r *Repository) CreateSession(ctx context.Context, userID int64, age time.Duration) (string, error) {
	if age <= 0 {
		return "", fmt.Errorf("session age must be positive")
	}
	var encoded string
	query := `SELECT password FROM auth_user WHERE id = ` + r.marker(1) + ` AND is_active = ` + r.marker(2)
	if err := r.db.QueryRowContext(ctx, query, userID, true).Scan(&encoded); err != nil {
		return "", fmt.Errorf("lookup session user: %w", err)
	}
	key, err := randomSessionKey()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(encoded))
	data, err := json.Marshal(sessionData{UserID: userID, PasswordHash: base64.RawStdEncoding.EncodeToString(digest[:])})
	if err != nil {
		return "", err
	}
	query = `INSERT INTO django_session (session_key, session_data, expire_date) VALUES (` + markers(r.engine, 3) + `)`
	if _, err := r.db.ExecContext(ctx, query, key, "go1:"+base64.RawStdEncoding.EncodeToString(data), time.Now().UTC().Add(age)); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return key, nil
}

func (r *Repository) AuthenticateSession(ctx context.Context, key string) (User, error) {
	if len(key) != 32 {
		return User{}, ErrInvalidCredentials
	}
	query := `SELECT s.session_data, u.id, u.username, u.email, u.is_active, u.is_staff, u.is_superuser, u.password
		FROM django_session AS s JOIN auth_user AS u ON u.id = ` + r.marker(1) +
		` WHERE s.session_key = ` + r.marker(2) + ` AND s.expire_date > ` + r.marker(3)
	// The user ID is stored inside session_data. Resolve it first without
	// trusting arbitrary data as an SQL identifier or bypassing expiry.
	var stored string
	lookup := `SELECT session_data FROM django_session WHERE session_key = ` + r.marker(1) + ` AND expire_date > ` + r.marker(2)
	if err := r.db.QueryRowContext(ctx, lookup, key, time.Now().UTC()).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("lookup session: %w", err)
	}
	if !strings.HasPrefix(stored, "go1:") {
		return User{}, ErrInvalidCredentials
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, "go1:"))
	if err != nil {
		return User{}, ErrInvalidCredentials
	}
	var session sessionData
	if err := json.Unmarshal(decoded, &session); err != nil || session.UserID <= 0 {
		return User{}, ErrInvalidCredentials
	}
	var user User
	var encoded, currentData string
	err = r.db.QueryRowContext(ctx, query, session.UserID, key, time.Now().UTC()).Scan(&currentData, &user.ID, &user.Username,
		&user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("load session user: %w", err)
	}
	digest := sha256.Sum256([]byte(encoded))
	want, err := base64.RawStdEncoding.DecodeString(session.PasswordHash)
	if !user.IsActive || err != nil || len(want) != len(digest) || subtle.ConstantTimeCompare(want, digest[:]) != 1 || currentData != stored {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (r *Repository) DeleteSession(ctx context.Context, key string) error {
	query := `DELETE FROM django_session WHERE session_key = ` + r.marker(1)
	if _, err := r.db.ExecContext(ctx, query, key); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func randomSessionKey() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var key [32]byte
	var random [32]byte
	for i := 0; i < len(key); {
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate session key: %w", err)
		}
		for _, value := range random {
			if value < 252 { // 252 is the largest multiple of 36 below 256.
				key[i] = alphabet[int(value)%len(alphabet)]
				i++
				if i == len(key) {
					break
				}
			}
		}
	}
	return string(key[:]), nil
}
