package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type User struct {
	ID          int64
	Username    string
	Email       string
	IsActive    bool
	IsStaff     bool
	IsSuperuser bool
}

type Repository struct {
	db     *sql.DB
	engine string
}

func NewRepository(db *sql.DB, engine string) *Repository {
	return &Repository{db: db, engine: engine}
}

func (r *Repository) marker(n int) string {
	if r.engine == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (r *Repository) AuthenticatePassword(ctx context.Context, username, password string) (User, error) {
	query := `SELECT id, username, email, is_active, is_staff, is_superuser, password FROM auth_user WHERE username = ` + r.marker(1)
	var user User
	var encoded string
	err := r.db.QueryRowContext(ctx, query, username).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return User{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(password, encoded)
	if err != nil {
		return User{}, fmt.Errorf("verify password for user %d: %w", user.ID, err)
	}
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (r *Repository) AuthenticateToken(ctx context.Context, token string) (User, error) {
	query := `SELECT u.id, u.username, u.email, u.is_active, u.is_staff, u.is_superuser
		FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id = t.user_id WHERE t.key = ` + r.marker(1)
	var user User
	err := r.db.QueryRowContext(ctx, query, token).Scan(&user.ID, &user.Username, &user.Email, &user.IsActive, &user.IsStaff, &user.IsSuperuser)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup API token: %w", err)
	}
	if !user.IsActive {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

// ParseTokenAuthorization accepts the two schemes enabled by the upstream
// DRF authentication class. Empty or unrelated headers are not token auth.
func ParseTokenAuthorization(header string) (token string, present bool, err error) {
	fields := strings.Fields(header)
	if len(fields) == 0 || (strings.ToLower(fields[0]) != "token" && strings.ToLower(fields[0]) != "bearer") {
		return "", false, nil
	}
	if len(fields) != 2 || fields[1] == "" {
		return "", true, ErrInvalidCredentials
	}
	return fields[1], true, nil
}
