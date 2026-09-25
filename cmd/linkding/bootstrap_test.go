package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func openBootstrapDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestEnsureInitialSuperuserCreatesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	cfg := config.Config{DBEngine: "sqlite", SuperuserName: "initial-admin", SuperuserPassword: "secret-password"}
	for range 2 {
		if err := ensureInitialSuperuser(ctx, db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	var count, staff, superuser int
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(is_staff), max(is_superuser) FROM auth_user WHERE username = ?`, cfg.SuperuserName).Scan(&count, &staff, &superuser); err != nil {
		t.Fatal(err)
	}
	if count != 1 || staff != 1 || superuser != 1 {
		t.Fatalf("created user: count=%d staff=%d superuser=%d", count, staff, superuser)
	}
	if _, err := auth.NewRepository(db, "sqlite").AuthenticatePassword(ctx, cfg.SuperuserName, cfg.SuperuserPassword); err != nil {
		t.Fatalf("created password cannot authenticate: %v", err)
	}
}

func TestEnsureInitialSuperuserLeavesExistingUserUntouched(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	repo := auth.NewRepository(db, "sqlite")
	existing, err := repo.CreateUser(ctx, auth.NewUser{Username: "initial-admin", Password: "original-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET email = 'owner@example.com', is_staff = 0, is_superuser = 0 WHERE id = ?`, existing.ID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE id = ?`, existing.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := ensureInitialSuperuser(ctx, db, config.Config{DBEngine: "sqlite", SuperuserName: "initial-admin", SuperuserPassword: "replacement-password"}); err != nil {
		t.Fatal(err)
	}
	var count, staff, superuser int
	var password, email string
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(is_staff), max(is_superuser), max(password), max(email) FROM auth_user WHERE username = 'initial-admin'`).Scan(&count, &staff, &superuser, &password, &email); err != nil {
		t.Fatal(err)
	}
	if count != 1 || staff != 0 || superuser != 0 || password != before || email != "owner@example.com" {
		t.Fatalf("existing user changed: count=%d staff=%d superuser=%d email=%q password-preserved=%v", count, staff, superuser, email, password == before)
	}
}

func TestEnsureInitialSuperuserWithoutPasswordUsesUnusablePassword(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	const username = "passwordless-admin"
	if err := ensureInitialSuperuser(ctx, db, config.Config{DBEngine: "sqlite", SuperuserName: username}); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := db.QueryRowContext(ctx, `SELECT password FROM auth_user WHERE username = ?`, username).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "!") {
		t.Fatalf("password marker is usable: prefix=%q", encoded[:min(1, len(encoded))])
	}
	if valid, err := auth.VerifyPassword("any-password", encoded); err != nil || valid {
		t.Fatalf("unusable password verified: valid=%v err=%v", valid, err)
	}
}

func TestEnsureInitialSuperuserWithoutNameIsNoop(t *testing.T) {
	if err := ensureInitialSuperuser(context.Background(), nil, config.Config{}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureInitialSuperuserPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DBEngine:          "postgres",
		SuperuserName:     fmt.Sprintf("bootstrap_admin_%d", time.Now().UnixNano()),
		SuperuserPassword: "bootstrap-password",
	}
	if err := ensureInitialSuperuser(ctx, db, cfg); err != nil {
		t.Fatal(err)
	}
	if err := ensureInitialSuperuser(ctx, db, cfg); err != nil {
		t.Fatalf("repeat bootstrap: %v", err)
	}
	var count, staff, superuser int
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(is_staff::int), max(is_superuser::int) FROM auth_user WHERE username = $1`, cfg.SuperuserName).Scan(&count, &staff, &superuser); err != nil {
		t.Fatal(err)
	}
	if count != 1 || staff != 1 || superuser != 1 {
		t.Fatalf("created PostgreSQL user: count=%d staff=%d superuser=%d", count, staff, superuser)
	}
	if _, err := auth.NewRepository(db, "postgres").AuthenticatePassword(ctx, cfg.SuperuserName, cfg.SuperuserPassword); err != nil {
		t.Fatalf("PostgreSQL password cannot authenticate: %v", err)
	}
	var userID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username = $1`, cfg.SuperuserName).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DELETE FROM bookmarks_userprofile WHERE user_id = $1`, userID); err != nil {
			t.Errorf("delete bootstrap profile: %v", err)
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM auth_user WHERE id = $1`, userID); err != nil {
			t.Errorf("delete bootstrap user: %v", err)
		}
	})
}
