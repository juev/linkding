package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestMigratedUserPasswordAndTokenStayValid(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	const encoded = "pbkdf2_sha256$1200000$0ECrmJ59dYW6pHDpMJOaVI$qwaozCPlUlj7Qu9Jb2C9HWxOfTZRfZ7dUQRDzx1ovIw="
	_, err = db.ExecContext(ctx, `INSERT INTO auth_user
		(id, password, is_superuser, username, last_name, email, is_staff, is_active, date_joined, first_name)
		VALUES (1, ?, 1, 'alice', '', 'alice@example.com', 1, 1, '2026-09-25 10:00:00', '')`, encoded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id)
		VALUES ('0123456789abcdef0123456789abcdef01234567', 'Existing client', '2026-09-25 10:00:00', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRepository(db, "sqlite")
	user, err := r.AuthenticatePassword(ctx, "alice", "parity-password")
	if err != nil || user.ID != 1 || !user.IsSuperuser {
		t.Fatalf("password login: user=%+v err=%v", user, err)
	}
	if _, err := r.AuthenticatePassword(ctx, "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := r.AuthenticatePassword(ctx, "Alice", "parity-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("case changed username: %v", err)
	}
	for _, scheme := range []string{"Token", "Bearer"} {
		token, present, err := ParseTokenAuthorization(scheme + " 0123456789abcdef0123456789abcdef01234567")
		if err != nil || !present {
			t.Fatalf("parse %s: token=%q present=%v err=%v", scheme, token, present, err)
		}
		user, err := r.AuthenticateToken(ctx, token)
		if err != nil || user.ID != 1 {
			t.Fatalf("authenticate %s: user=%+v err=%v", scheme, user, err)
		}
	}
	if _, err := r.AuthenticateToken(ctx, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET is_active = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateToken(ctx, "0123456789abcdef0123456789abcdef01234567"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inactive token owner: %v", err)
	}
}

func TestTokenAuthorizationHeader(t *testing.T) {
	cases := []struct {
		header  string
		present bool
		valid   bool
	}{
		{"token abc", true, true},
		{"BEARER abc", true, true},
		{"Token", true, false},
		{"Bearer abc def", true, false},
		{"Basic abc", false, true},
		{"", false, true},
	}
	for _, tc := range cases {
		_, present, err := ParseTokenAuthorization(tc.header)
		if present != tc.present || (err == nil) != tc.valid {
			t.Fatalf("header %q: present=%v err=%v", tc.header, present, err)
		}
	}
}

func TestCreateUserCreatesPinnedDefaultProfile(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	r := NewRepository(db, "sqlite")
	user, err := r.CreateUser(ctx, NewUser{Username: "bob", Password: "parity-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticatePassword(ctx, "bob", "parity-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	p, err := r.GetProfile(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Theme != "auto" || p.BookmarkDateDisplay != "relative" || p.BookmarkLinkTarget != "_blank" ||
		p.WebArchiveIntegration != "disabled" || p.TagSearch != "strict" || p.EnableSharing || p.EnableFavicons ||
		string(p.SearchPreferences) != "{}" || p.Version != "1.47.0" {
		t.Fatalf("new profile differs from pinned reference: %+v", p)
	}
}

func TestOIDCUserLookupPreservesExistingAccount(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, "sqlite")
	existing, err := repo.CreateUser(ctx, NewUser{Username: "alice", Email: "Person@Example.com", Password: "parity-password"})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := repo.GetOrCreateOIDCUser(ctx, "person@example.com", "different")
	if err != nil || matched.ID != existing.ID || matched.Username != "alice" {
		t.Fatalf("existing email: %+v %v", matched, err)
	}
	if _, err := repo.AuthenticatePassword(ctx, "alice", "parity-password"); err != nil {
		t.Fatalf("existing password changed: %v", err)
	}
	created, err := repo.GetOrCreateOIDCUser(ctx, "new@example.com", "newuser")
	if err != nil || created.Username != "newuser" {
		t.Fatalf("new OIDC account: %+v %v", created, err)
	}
	if _, err := repo.AuthenticatePassword(ctx, "newuser", "password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("OIDC account has usable local password: %v", err)
	}
	if _, err := repo.CreateUser(ctx, NewUser{Username: "duplicate", Email: "person@example.com", Password: "password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetOrCreateOIDCUser(ctx, "person@example.com", "different"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("duplicate email was selected: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET is_active=0 WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetOrCreateOIDCUser(ctx, "new@example.com", "newuser"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("inactive OIDC user accepted: %v", err)
	}
}

func TestCreateUserPostgres(t *testing.T) {
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
	r := NewRepository(db, "postgres")
	user, err := r.CreateUser(ctx, NewUser{Username: fmt.Sprintf("postgres_parity_%d", time.Now().UnixNano()), Password: "parity-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_userprofile WHERE user_id = $1`, user.ID); err != nil {
			t.Errorf("delete test profile: %v", err)
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM auth_user WHERE id = $1`, user.ID); err != nil {
			t.Errorf("delete test user: %v", err)
		}
	})
	if _, err := r.AuthenticatePassword(ctx, user.Username, "parity-password"); err != nil {
		t.Fatalf("PostgreSQL password login: %v", err)
	}
	before := time.Now().UTC()
	if err := r.RecordLogin(ctx, user.ID); err != nil {
		t.Fatalf("PostgreSQL last login update: %v", err)
	}
	var lastLogin sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT last_login FROM auth_user WHERE id = $1`, user.ID).Scan(&lastLogin); err != nil {
		t.Fatalf("PostgreSQL last login query: %v", err)
	}
	if !lastLogin.Valid || lastLogin.Time.Before(before) || lastLogin.Time.After(time.Now().UTC()) {
		t.Fatalf("PostgreSQL last login = %v, want current login time", lastLogin)
	}
	if _, err := r.GetProfile(ctx, user.ID); err != nil {
		t.Fatalf("PostgreSQL default profile: %v", err)
	}
}
