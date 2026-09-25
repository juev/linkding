package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NewUser struct {
	Username    string
	Password    string
	Email       string
	IsStaff     bool
	IsSuperuser bool
}

// CreateUser writes the Django user and its profile in one transaction.
func (r *Repository) CreateUser(ctx context.Context, input NewUser) (User, error) {
	if input.Username == "" || len([]rune(input.Username)) > 150 {
		return User{}, fmt.Errorf("username must contain 1 to 150 characters")
	}
	if input.Password == "" {
		return User{}, fmt.Errorf("password must not be empty")
	}
	encoded, err := HashPassword(input.Password)
	if err != nil {
		return User{}, err
	}
	return r.createUserWithHash(ctx, input, encoded)
}

// CreateUnusableUser mirrors Django Admin's disabled-password option.
func (r *Repository) CreateUnusableUser(ctx context.Context, input NewUser) (User, error) {
	if input.Username == "" || len([]rune(input.Username)) > 150 {
		return User{}, fmt.Errorf("username must contain 1 to 150 characters")
	}
	encoded, err := MakeUnusablePassword()
	if err != nil {
		return User{}, err
	}
	return r.createUserWithHash(ctx, input, encoded)
}

// CreateUserTx creates a user and profile in the caller's transaction so an
// administrative action can commit its audit entry with the new account.
func (r *Repository) CreateUserTx(ctx context.Context, tx *sql.Tx, input NewUser, usablePassword bool) (User, error) {
	if input.Username == "" || len([]rune(input.Username)) > 150 {
		return User{}, fmt.Errorf("username must contain 1 to 150 characters")
	}
	var encoded string
	var err error
	if usablePassword {
		if input.Password == "" {
			return User{}, fmt.Errorf("password must not be empty")
		}
		encoded, err = HashPassword(input.Password)
	} else {
		encoded, err = MakeUnusablePassword()
	}
	if err != nil {
		return User{}, err
	}
	return r.createUserWithHashTx(ctx, tx, input, encoded)
}

// MakeUnusablePassword uses Django's unusable-password prefix and suffix length.
func MakeUnusablePassword() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var suffix [40]byte
	var random [40]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate unusable password: %w", err)
	}
	for index, value := range random {
		suffix[index] = alphabet[int(value)%len(alphabet)]
	}
	return "!" + string(suffix[:]), nil
}

// GetOrCreateRemoteUser mirrors Django RemoteUserBackend's default behavior.
// Proxy-created accounts have no usable local password.
func (r *Repository) GetOrCreateRemoteUser(ctx context.Context, username string) (User, error) {
	if username == "" {
		return User{}, ErrInvalidCredentials
	}
	query := `SELECT id,username,email,is_active,is_staff,is_superuser FROM auth_user WHERE username = ` + r.marker(1)
	lookup := func() (User, error) {
		var user User
		err := r.db.QueryRowContext(ctx, query, username).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser)
		if err == nil && !user.IsActive {
			return User{}, ErrInvalidCredentials
		}
		return user, err
	}
	if user, err := lookup(); err == nil || errors.Is(err, ErrInvalidCredentials) {
		return user, err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	if len([]rune(username)) > 150 {
		return User{}, ErrInvalidCredentials
	}
	user, err := r.createUserWithHash(ctx, NewUser{Username: username}, "!")
	if err == nil {
		return user, nil
	}
	if existing, lookupErr := lookup(); lookupErr == nil || errors.Is(lookupErr, ErrInvalidCredentials) {
		return existing, lookupErr
	}
	return User{}, err
}

// GetOrCreateOIDCUser follows the upstream email lookup and creates an account
// with an unusable password when no matching account exists.
func (r *Repository) GetOrCreateOIDCUser(ctx context.Context, email, username string) (User, error) {
	if email != "" {
		condition := "ld_ci_equal(email, " + r.marker(1) + ") = 1"
		if r.engine == "postgres" {
			condition = "LOWER(email) = LOWER(" + r.marker(1) + ")"
		}
		query := `SELECT id,username,email,is_active,is_staff,is_superuser FROM auth_user WHERE ` + condition + ` ORDER BY id LIMIT 2`
		rows, err := r.db.QueryContext(ctx, query, email)
		if err != nil {
			return User{}, err
		}
		var matches []User
		for rows.Next() {
			var user User
			if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser); err != nil {
				rows.Close()
				return User{}, err
			}
			matches = append(matches, user)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return User{}, err
		}
		if len(matches) > 1 || len(matches) == 1 && !matches[0].IsActive {
			return User{}, ErrInvalidCredentials
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
	}
	if username == "" || len([]rune(username)) > 150 {
		return User{}, ErrInvalidCredentials
	}
	return r.createUserWithHash(ctx, NewUser{Username: username, Email: email}, "!")
}

func (r *Repository) createUserWithHash(ctx context.Context, input NewUser, encoded string) (User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin user creation: %w", err)
	}
	defer tx.Rollback()
	user, err := r.createUserWithHashTx(ctx, tx, input, encoded)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user creation: %w", err)
	}
	return user, nil
}

func (r *Repository) createUserWithHashTx(ctx context.Context, tx *sql.Tx, input NewUser, encoded string) (User, error) {
	user := User{Username: input.Username, Email: input.Email, IsActive: true, IsStaff: input.IsStaff, IsSuperuser: input.IsSuperuser}
	query := `INSERT INTO auth_user (password, is_superuser, username, last_name, email, is_staff, is_active, date_joined, first_name)
		VALUES (` + markers(r.engine, 9) + `) RETURNING id`
	err := tx.QueryRowContext(ctx, query, encoded, user.IsSuperuser, user.Username, "", user.Email, user.IsStaff,
		user.IsActive, time.Now().UTC(), "").Scan(&user.ID)
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	columns := []string{
		"user_id", "theme", "bookmark_date_display", "bookmark_link_target", "web_archive_integration",
		"tag_search", "enable_sharing", "enable_public_sharing", "enable_favicons", "display_url",
		"permanent_notes", "search_preferences", "bookmark_description_display", "bookmark_description_max_lines",
		"tag_grouping", "enable_preview_images", "display_view_bookmark_action", "display_edit_bookmark_action",
		"display_archive_bookmark_action", "display_remove_bookmark_action", "custom_css", "custom_css_hash",
		"auto_tagging_rules", "enable_automatic_html_snapshots", "default_mark_unread", "default_mark_shared",
		"items_per_page", "sticky_pagination", "collapse_side_panel", "hide_bundles", "legacy_search",
	}
	values := []any{
		user.ID, "auto", "relative", "_blank", "disabled", "strict", false, false, false, false,
		false, "{}", "inline", 1, "alphabetical", false, true, true, true, true, "", "", "", true,
		false, false, 30, false, false, false, false,
	}
	query = `INSERT INTO bookmarks_userprofile (` + strings.Join(columns, ", ") + `) VALUES (` + markers(r.engine, len(values)) + `)`
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return User{}, fmt.Errorf("insert user profile: %w", err)
	}
	return user, nil
}

func markers(engine string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		if engine == "postgres" {
			parts[i] = fmt.Sprintf("$%d", i+1)
		} else {
			parts[i] = "?"
		}
	}
	return strings.Join(parts, ", ")
}
