package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"
)

// ErrLastPasskey is returned when deleting a passkey would leave the user
// without a way to sign in while password sign-in is disabled.
var ErrLastPasskey = errors.New("cannot delete the last passkey while password sign-in is disabled")

// ErrPasskeyNotFound is returned when the passkey does not exist or belongs
// to another user.
var ErrPasskeyNotFound = errors.New("passkey not found")

// ErrInvalidChallenge is returned for unknown, expired, reused, or foreign
// WebAuthn ceremony keys.
var ErrInvalidChallenge = errors.New("invalid or expired passkey challenge")

// ErrInvalidRecoveryToken is returned for unknown, expired, or used tokens.
var ErrInvalidRecoveryToken = errors.New("invalid or expired recovery token")

const passkeyNameMaxLength = 128

// Passkey is a stored WebAuthn credential of one user.
type Passkey struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
	Credential webauthn.Credential
}

// NormalizePasskeyName trims the user-supplied name and applies the default.
func NormalizePasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Passkey", nil
	}
	if utf8.RuneCountInString(name) > passkeyNameMaxLength {
		return "", fmt.Errorf("passkey name must have at most %d characters", passkeyNameMaxLength)
	}
	return name, nil
}

// PasskeyCredentialID is the stored text form of a WebAuthn credential ID.
func PasskeyCredentialID(id []byte) string {
	return base64.RawURLEncoding.EncodeToString(id)
}

func (r *Repository) ListPasskeys(ctx context.Context, userID int64) ([]Passkey, error) {
	query := `SELECT id, name, credential, created_at, last_used_at FROM linkding_passkey WHERE user_id = ` + r.marker(1) + ` ORDER BY created_at, id`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	defer rows.Close()
	var passkeys []Passkey
	for rows.Next() {
		var passkey Passkey
		var encoded string
		if err := rows.Scan(&passkey.ID, &passkey.Name, &encoded, &passkey.CreatedAt, &passkey.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan passkey: %w", err)
		}
		if err := json.Unmarshal([]byte(encoded), &passkey.Credential); err != nil {
			return nil, fmt.Errorf("decode passkey %d: %w", passkey.ID, err)
		}
		passkeys = append(passkeys, passkey)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	return passkeys, nil
}

func (r *Repository) AddPasskey(ctx context.Context, userID int64, name string, credential webauthn.Credential) error {
	name, err := NormalizePasskeyName(name)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode passkey: %w", err)
	}
	query := `INSERT INTO linkding_passkey (user_id, credential_id, name, credential, created_at) VALUES (` + markers(r.engine, 5) + `)`
	if _, err := r.db.ExecContext(ctx, query, userID, PasskeyCredentialID(credential.ID), name, string(encoded), time.Now().UTC()); err != nil {
		return fmt.Errorf("store passkey: %w", err)
	}
	return nil
}

// DeletePasskey removes one passkey of the user and returns its name. With
// keepLast set, the user's only passkey is kept and ErrLastPasskey returned.
func (r *Repository) DeletePasskey(ctx context.Context, userID, passkeyID int64, keepLast bool) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin passkey deletion: %w", err)
	}
	defer tx.Rollback()
	if r.engine == "postgres" {
		// Serialize concurrent deletions of the same user's passkeys so that
		// two requests cannot both observe a second remaining passkey.
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE id = $1 FOR UPDATE`, userID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", ErrPasskeyNotFound
			}
			return "", fmt.Errorf("lock passkey owner: %w", err)
		}
	}
	var name string
	query := `SELECT name FROM linkding_passkey WHERE id = ` + r.marker(1) + ` AND user_id = ` + r.marker(2)
	if err := tx.QueryRowContext(ctx, query, passkeyID, userID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrPasskeyNotFound
		}
		return "", fmt.Errorf("lookup passkey: %w", err)
	}
	if keepLast {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM linkding_passkey WHERE user_id = `+r.marker(1), userID).Scan(&count); err != nil {
			return "", fmt.Errorf("count passkeys: %w", err)
		}
		if count <= 1 {
			return "", ErrLastPasskey
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM linkding_passkey WHERE id = `+r.marker(1)+` AND user_id = `+r.marker(2), passkeyID, userID); err != nil {
		return "", fmt.Errorf("delete passkey: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit passkey deletion: %w", err)
	}
	return name, nil
}

// PasskeyOwner returns the active user that owns the credential.
func (r *Repository) PasskeyOwner(ctx context.Context, credentialID []byte) (User, error) {
	query := `SELECT u.id, u.username, u.email, u.is_active, u.is_staff, u.is_superuser
		FROM linkding_passkey AS p JOIN auth_user AS u ON u.id = p.user_id WHERE p.credential_id = ` + r.marker(1)
	var user User
	err := r.db.QueryRowContext(ctx, query, PasskeyCredentialID(credentialID)).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup passkey owner: %w", err)
	}
	if !user.IsActive {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// RecordPasskeyLogin stores the updated sign counter and flags after a
// successful assertion.
func (r *Repository) RecordPasskeyLogin(ctx context.Context, userID int64, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode passkey: %w", err)
	}
	query := `UPDATE linkding_passkey SET credential = ` + r.marker(1) + `, last_used_at = ` + r.marker(2) +
		` WHERE credential_id = ` + r.marker(3) + ` AND user_id = ` + r.marker(4)
	result, err := r.db.ExecContext(ctx, query, string(encoded), time.Now().UTC(), PasskeyCredentialID(credential.ID), userID)
	if err != nil {
		return fmt.Errorf("update passkey: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check passkey update: %w", err)
	}
	if count != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

// CountActiveUsersWithoutPasskey counts users who cannot sign in with a
// passkey. With password sign-in disabled they depend on OIDC, the auth proxy,
// or the passkey_recovery command.
func (r *Repository) CountActiveUsersWithoutPasskey(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM auth_user AS u WHERE u.is_active = ` + r.marker(1) +
		` AND NOT EXISTS (SELECT 1 FROM linkding_passkey AS p WHERE p.user_id = u.id)`
	var count int
	if err := r.db.QueryRowContext(ctx, query, true).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users without passkey: %w", err)
	}
	return count, nil
}

// ActiveUserByUsername loads an active user for administrative commands.
func (r *Repository) ActiveUserByUsername(ctx context.Context, username string) (User, error) {
	query := `SELECT id, username, email, is_active, is_staff, is_superuser FROM auth_user WHERE username = ` + r.marker(1)
	var user User
	err := r.db.QueryRowContext(ctx, query, username).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("user %q not found", username)
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return User{}, fmt.Errorf("user %q is inactive", username)
	}
	return user, nil
}

func (r *Repository) activeUserByID(ctx context.Context, userID int64) (User, error) {
	query := `SELECT id, username, email, is_active, is_staff, is_superuser FROM auth_user WHERE id = ` + r.marker(1)
	var user User
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// SaveWebAuthnChallenge stores ceremony state between the begin and finish
// requests and returns the key the browser presents on finish.
func (r *Repository) SaveWebAuthnChallenge(ctx context.Context, kind string, userID int64, data []byte, expires time.Time) (string, error) {
	now := time.Now().UTC()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM linkding_webauthn_challenge WHERE expires_at <= `+r.marker(1), now); err != nil {
		return "", fmt.Errorf("delete expired passkey challenges: %w", err)
	}
	key, err := randomToken()
	if err != nil {
		return "", err
	}
	var owner any
	if userID > 0 {
		owner = userID
	}
	query := `INSERT INTO linkding_webauthn_challenge (challenge_key, kind, user_id, session_data, expires_at) VALUES (` + markers(r.engine, 5) + `)`
	if _, err := r.db.ExecContext(ctx, query, key, kind, owner, string(data), expires.UTC()); err != nil {
		return "", fmt.Errorf("store passkey challenge: %w", err)
	}
	return key, nil
}

// TakeWebAuthnChallenge consumes the ceremony state. A key works once, only
// for its kind and user, and only until it expires.
func (r *Repository) TakeWebAuthnChallenge(ctx context.Context, key, kind string, userID int64) ([]byte, error) {
	if key == "" {
		return nil, ErrInvalidChallenge
	}
	var storedKind, data string
	var owner sql.NullInt64
	var expires time.Time
	query := `SELECT kind, user_id, session_data, expires_at FROM linkding_webauthn_challenge WHERE challenge_key = ` + r.marker(1)
	if err := r.db.QueryRowContext(ctx, query, key).Scan(&storedKind, &owner, &data, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidChallenge
		}
		return nil, fmt.Errorf("lookup passkey challenge: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM linkding_webauthn_challenge WHERE challenge_key = `+r.marker(1), key)
	if err != nil {
		return nil, fmt.Errorf("consume passkey challenge: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, ErrInvalidChallenge
	}
	if storedKind != kind || owner.Int64 != userID || !expires.After(time.Now()) {
		return nil, ErrInvalidChallenge
	}
	return []byte(data), nil
}

// CreateRecoveryToken issues a single-use sign-in token. Only its SHA-256
// digest is stored.
func (r *Repository) CreateRecoveryToken(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("recovery token lifetime must be positive")
	}
	now := time.Now().UTC()
	if _, err := r.db.ExecContext(ctx, `DELETE FROM linkding_recovery_token WHERE expires_at <= `+r.marker(1), now); err != nil {
		return "", fmt.Errorf("delete expired recovery tokens: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	query := `INSERT INTO linkding_recovery_token (token_hash, user_id, created_at, expires_at) VALUES (` + markers(r.engine, 4) + `)`
	if _, err := r.db.ExecContext(ctx, query, recoveryTokenHash(token), userID, now, now.Add(ttl)); err != nil {
		return "", fmt.Errorf("store recovery token: %w", err)
	}
	return token, nil
}

// ConsumeRecoveryToken invalidates the token and returns its active user.
func (r *Repository) ConsumeRecoveryToken(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrInvalidRecoveryToken
	}
	hash := recoveryTokenHash(token)
	var userID int64
	var expires time.Time
	query := `SELECT user_id, expires_at FROM linkding_recovery_token WHERE token_hash = ` + r.marker(1)
	if err := r.db.QueryRowContext(ctx, query, hash).Scan(&userID, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrInvalidRecoveryToken
		}
		return User{}, fmt.Errorf("lookup recovery token: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM linkding_recovery_token WHERE token_hash = `+r.marker(1), hash)
	if err != nil {
		return User{}, fmt.Errorf("consume recovery token: %w", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return User{}, ErrInvalidRecoveryToken
	}
	if !expires.After(time.Now()) {
		return User{}, ErrInvalidRecoveryToken
	}
	user, err := r.activeUserByID(ctx, userID)
	if errors.Is(err, ErrInvalidCredentials) {
		return User{}, ErrInvalidRecoveryToken
	}
	return user, err
}

func recoveryTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
