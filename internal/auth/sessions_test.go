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

func TestNewSessionsRequireActiveUserAndCurrentPassword(t *testing.T) {
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
	user, err := r.CreateUser(ctx, NewUser{Username: "alice", Password: "old-password"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := r.CreateSession(ctx, user.ID, 24*time.Hour)
	if err != nil || len(key) != 32 {
		t.Fatalf("create session: key=%q err=%v", key, err)
	}
	got, err := r.AuthenticateSession(ctx, key)
	if err != nil || got.ID != user.ID {
		t.Fatalf("authenticate session: user=%+v err=%v", got, err)
	}
	if _, err := r.AuthenticateSession(ctx, "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong session key: %v", err)
	}
	oldKey := "dddddddddddddddddddddddddddddddd"
	if _, err := db.ExecContext(ctx, `INSERT INTO django_session (session_key, session_data, expire_date) VALUES (?, ?, ?)`, oldKey, "Django-signed-payload", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSession(ctx, oldKey); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old Django session should require relogin: %v", err)
	}
	newHash, err := HashPassword("new-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET password = ? WHERE id = ?`, newHash, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSession(ctx, key); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("password change should invalidate old session: %v", err)
	}
	newKey, err := r.CreateSession(ctx, user.ID, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSession(ctx, newKey); err != nil {
		t.Fatalf("new session after password change: %v", err)
	}
	if err := r.DeleteSession(ctx, newKey); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateSession(ctx, newKey); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deleted session: %v", err)
	}
	if SessionCookieName != "ld_sessionid" || CSRFCookieName != "ld_csrftoken" {
		t.Fatalf("cookie names differ from pinned server")
	}
}

func TestSessionPostgres(t *testing.T) {
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
	user, err := r.CreateUser(ctx, NewUser{Username: fmt.Sprintf("session_parity_%d", time.Now().UnixNano()), Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("cleanup PostgreSQL session fixture: %v", err)
			}
		}
	})
	key, err := r.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.DeleteSession(context.Background(), key); err != nil {
			t.Errorf("delete PostgreSQL session: %v", err)
		}
	})
	got, err := r.AuthenticateSession(ctx, key)
	if err != nil || got.ID != user.ID {
		t.Fatalf("PostgreSQL session: user=%+v err=%v", got, err)
	}
}
