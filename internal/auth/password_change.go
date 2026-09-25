package auth

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

//go:embed common-passwords.txt.gz
var commonPasswordsGzip []byte

var commonPasswordsOnce sync.Once
var commonPasswords map[string]struct{}
var commonPasswordsError error

type PasswordChangeError struct{ Field, Message string }

func (e PasswordChangeError) Error() string { return e.Message }

func (r *Repository) ChangePassword(ctx context.Context, userID int64, sessionKey, oldPassword, newPassword string) error {
	var encoded, username, firstName, lastName, email string
	query := `SELECT password,username,first_name,last_name,email FROM auth_user WHERE id = ` + r.marker(1)
	if err := r.db.QueryRowContext(ctx, query, userID).Scan(&encoded, &username, &firstName, &lastName, &email); err != nil {
		return err
	}
	correct, err := VerifyPassword(oldPassword, encoded)
	if err != nil {
		return err
	}
	if !correct {
		return PasswordChangeError{Field: "old_password", Message: "Your old password was entered incorrectly. Please enter it again."}
	}
	if err := validateNewPassword(newPassword, []string{username, firstName, lastName, email}); err != nil {
		return err
	}
	newEncoded, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldData string
	query = `SELECT session_data FROM django_session WHERE session_key = ` + r.marker(1)
	if err := tx.QueryRowContext(ctx, query, sessionKey).Scan(&oldData); err != nil {
		return err
	}
	if !strings.HasPrefix(oldData, "go1:") {
		return ErrInvalidCredentials
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(oldData, "go1:"))
	if err != nil {
		return ErrInvalidCredentials
	}
	var session sessionData
	if err := json.Unmarshal(decoded, &session); err != nil || session.UserID != userID {
		return ErrInvalidCredentials
	}
	oldDigest := sha256.Sum256([]byte(encoded))
	if session.PasswordHash != base64.RawStdEncoding.EncodeToString(oldDigest[:]) {
		return ErrInvalidCredentials
	}
	query = `UPDATE auth_user SET password = ` + r.marker(1) + ` WHERE id = ` + r.marker(2) + ` AND password = ` + r.marker(3)
	result, err := tx.ExecContext(ctx, query, newEncoded, userID, encoded)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrInvalidCredentials
	}
	newDigest := sha256.Sum256([]byte(newEncoded))
	session.PasswordHash = base64.RawStdEncoding.EncodeToString(newDigest[:])
	updated, err := json.Marshal(session)
	if err != nil {
		return err
	}
	query = `UPDATE django_session SET session_data = ` + r.marker(1) + ` WHERE session_key = ` + r.marker(2) + ` AND session_data = ` + r.marker(3)
	result, err = tx.ExecContext(ctx, query, "go1:"+base64.RawStdEncoding.EncodeToString(updated), sessionKey, oldData)
	if err != nil {
		return err
	}
	changed, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrInvalidCredentials
	}
	return tx.Commit()
}

func validateNewPassword(password string, attributes []string) error {
	if len([]rune(password)) < 8 {
		return PasswordChangeError{Field: "new_password2", Message: "This password is too short. It must contain at least 8 characters."}
	}
	allDigits := true
	for _, r := range password {
		if !unicode.IsDigit(r) {
			allDigits = false
			break
		}
	}
	if allDigits {
		return PasswordChangeError{Field: "new_password2", Message: "This password is entirely numeric."}
	}
	commonPasswordsOnce.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(commonPasswordsGzip))
		if err != nil {
			commonPasswordsError = err
			return
		}
		defer reader.Close()
		content, err := io.ReadAll(reader)
		if err != nil {
			commonPasswordsError = err
			return
		}
		commonPasswords = make(map[string]struct{})
		for _, line := range strings.Split(string(content), "\n") {
			commonPasswords[strings.TrimSpace(line)] = struct{}{}
		}
	})
	if commonPasswordsError != nil {
		return fmt.Errorf("load common passwords: %w", commonPasswordsError)
	}
	if _, found := commonPasswords[strings.ToLower(strings.TrimSpace(password))]; found {
		return PasswordChangeError{Field: "new_password2", Message: "This password is too common."}
	}
	lower := []rune(strings.ToLower(password))
	for _, attribute := range attributes {
		if attribute == "" {
			continue
		}
		for _, part := range attributeParts(attribute) {
			value := []rune(part)
			if len(value) == 0 {
				continue
			}
			if len(lower) >= 10*len(value) && float64(len(value)) < 0.35*float64(len(lower)) {
				continue
			}
			if quickSimilarity(lower, value) >= 0.7 {
				return PasswordChangeError{Field: "new_password2", Message: "The password is too similar to your personal information."}
			}
		}
	}
	return nil
}

// ValidateNewPassword applies the configured Django-compatible password rules.
func ValidateNewPassword(password string, attributes ...string) error {
	return validateNewPassword(password, attributes)
}

func attributeParts(value string) []string {
	lower := strings.ToLower(value)
	result := []string{}
	start := 0
	for index, r := range lower {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			continue
		}
		if index > start {
			result = append(result, lower[start:index])
		}
		start = index + len(string(r))
	}
	if start < len(lower) {
		result = append(result, lower[start:])
	}
	return append(result, lower)
}

func quickSimilarity(a, b []rune) float64 {
	counts := make(map[rune]int, len(b))
	for _, r := range b {
		counts[r]++
	}
	common := 0
	for _, r := range a {
		if counts[r] > 0 {
			counts[r]--
			common++
		}
	}
	return float64(2*common) / float64(len(a)+len(b))
}
