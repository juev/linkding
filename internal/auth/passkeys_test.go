package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestPasskeyRepositorySQLite(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	testPasskeyRepository(t, db, "sqlite")
}

func TestPasskeyRepositoryPostgres(t *testing.T) {
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
	testPasskeyRepository(t, db, "postgres")
}

func testPasskeyRepository(t *testing.T, db *sql.DB, engine string) {
	ctx := context.Background()
	r := NewRepository(db, engine)
	suffix := time.Now().UnixNano()
	newUser := func(name string) User {
		t.Helper()
		user, err := r.CreateUser(ctx, NewUser{Username: fmt.Sprintf("%s_%d", name, suffix), Password: "secret-password"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, query := range []string{`DELETE FROM bookmarks_userprofile WHERE user_id = `, `DELETE FROM auth_user WHERE id = `} {
				if _, err := db.ExecContext(context.Background(), query+r.marker(1), user.ID); err != nil {
					t.Errorf("cleanup user %d: %v", user.ID, err)
				}
			}
		})
		return user
	}
	alice := newUser("alice")
	bob := newUser("bob")

	withoutBefore, err := r.CountActiveUsersWithoutPasskey(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("passkeys belong to their owner", func(t *testing.T) {
		first := webauthn.Credential{ID: []byte(fmt.Sprintf("cred-a1-%d", suffix)), PublicKey: []byte{1, 2, 3}}
		second := webauthn.Credential{ID: []byte(fmt.Sprintf("cred-a2-%d", suffix)), PublicKey: []byte{4, 5, 6}}
		if err := r.AddPasskey(ctx, alice.ID, "  Laptop  ", first); err != nil {
			t.Fatal(err)
		}
		if err := r.AddPasskey(ctx, alice.ID, "", second); err != nil {
			t.Fatal(err)
		}
		if err := r.AddPasskey(ctx, bob.ID, "Stolen", first); err == nil {
			t.Fatal("the same credential ID was stored for a second user")
		}
		if err := r.AddPasskey(ctx, alice.ID, strings.Repeat("x", 129), webauthn.Credential{ID: []byte("long")}); err == nil {
			t.Fatal("a 129-character name was accepted")
		}
		passkeys, err := r.ListPasskeys(ctx, alice.ID)
		if err != nil || len(passkeys) != 2 {
			t.Fatalf("list passkeys: %+v err=%v", passkeys, err)
		}
		if passkeys[0].Name != "Laptop" || passkeys[1].Name != "Passkey" || string(passkeys[0].Credential.PublicKey) != "\x01\x02\x03" || passkeys[0].LastUsedAt.Valid {
			t.Fatalf("stored passkeys: %+v", passkeys)
		}
		owner, err := r.PasskeyOwner(ctx, first.ID)
		if err != nil || owner.ID != alice.ID {
			t.Fatalf("passkey owner: %+v err=%v", owner, err)
		}
		if _, err := r.PasskeyOwner(ctx, []byte("unknown")); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("unknown credential owner: %v", err)
		}
		first.Authenticator.SignCount = 7
		if err := r.RecordPasskeyLogin(ctx, alice.ID, first); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordPasskeyLogin(ctx, bob.ID, first); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login recorded for a foreign credential: %v", err)
		}
		passkeys, err = r.ListPasskeys(ctx, alice.ID)
		if err != nil || passkeys[0].Credential.Authenticator.SignCount != 7 || !passkeys[0].LastUsedAt.Valid {
			t.Fatalf("passkey after login: %+v err=%v", passkeys, err)
		}
		without, err := r.CountActiveUsersWithoutPasskey(ctx)
		if err != nil || without != withoutBefore-1 {
			t.Fatalf("users without passkey = %d, want %d (err=%v)", without, withoutBefore-1, err)
		}

		if _, err := r.DeletePasskey(ctx, bob.ID, passkeys[0].ID, false); !errors.Is(err, ErrPasskeyNotFound) {
			t.Fatalf("foreign passkey deletion: %v", err)
		}
		name, err := r.DeletePasskey(ctx, alice.ID, passkeys[0].ID, true)
		if err != nil || name != "Laptop" {
			t.Fatalf("delete one of two passkeys with keepLast: name=%q err=%v", name, err)
		}
		if _, err := r.DeletePasskey(ctx, alice.ID, passkeys[1].ID, true); !errors.Is(err, ErrLastPasskey) {
			t.Fatalf("last passkey deletion with keepLast: %v", err)
		}
		if remaining, err := r.ListPasskeys(ctx, alice.ID); err != nil || len(remaining) != 1 {
			t.Fatalf("refused deletion removed the passkey: %+v err=%v", remaining, err)
		}
		if _, err := r.DeletePasskey(ctx, alice.ID, passkeys[1].ID, false); err != nil {
			t.Fatalf("last passkey deletion without keepLast: %v", err)
		}
	})

	t.Run("challenges are single use and bound to kind and user", func(t *testing.T) {
		expires := time.Now().Add(time.Minute)
		key, err := r.SaveWebAuthnChallenge(ctx, "register", alice.ID, []byte(`{"challenge":"a"}`), expires)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.TakeWebAuthnChallenge(ctx, key, "register", bob.ID); !errors.Is(err, ErrInvalidChallenge) {
			t.Fatalf("challenge accepted for another user: %v", err)
		}
		// The failed attempt consumed the key as well.
		if _, err := r.TakeWebAuthnChallenge(ctx, key, "register", alice.ID); !errors.Is(err, ErrInvalidChallenge) {
			t.Fatalf("challenge reused after a failed attempt: %v", err)
		}
		key, err = r.SaveWebAuthnChallenge(ctx, "login", 0, []byte(`{"challenge":"b"}`), expires)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.TakeWebAuthnChallenge(ctx, key, "register", 0); !errors.Is(err, ErrInvalidChallenge) {
			t.Fatalf("login challenge accepted for registration: %v", err)
		}
		key, err = r.SaveWebAuthnChallenge(ctx, "login", 0, []byte(`{"challenge":"c"}`), expires)
		if err != nil {
			t.Fatal(err)
		}
		data, err := r.TakeWebAuthnChallenge(ctx, key, "login", 0)
		if err != nil || string(data) != `{"challenge":"c"}` {
			t.Fatalf("take challenge: %q err=%v", data, err)
		}
		if _, err := r.TakeWebAuthnChallenge(ctx, key, "login", 0); !errors.Is(err, ErrInvalidChallenge) {
			t.Fatalf("challenge reused: %v", err)
		}
		key, err = r.SaveWebAuthnChallenge(ctx, "login", 0, []byte(`{}`), time.Now().Add(-time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.TakeWebAuthnChallenge(ctx, key, "login", 0); !errors.Is(err, ErrInvalidChallenge) {
			t.Fatalf("expired challenge accepted: %v", err)
		}
	})

	t.Run("recovery tokens are single use and stored as hashes", func(t *testing.T) {
		token, err := r.CreateRecoveryToken(ctx, alice.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var stored int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM linkding_recovery_token WHERE token_hash = `+r.marker(1), token).Scan(&stored); err != nil || stored != 0 {
			t.Fatalf("plain token stored: count=%d err=%v", stored, err)
		}
		user, err := r.ConsumeRecoveryToken(ctx, token)
		if err != nil || user.ID != alice.ID {
			t.Fatalf("consume token: %+v err=%v", user, err)
		}
		if _, err := r.ConsumeRecoveryToken(ctx, token); !errors.Is(err, ErrInvalidRecoveryToken) {
			t.Fatalf("token reused: %v", err)
		}
		if _, err := r.ConsumeRecoveryToken(ctx, "unknown"); !errors.Is(err, ErrInvalidRecoveryToken) {
			t.Fatalf("unknown token: %v", err)
		}
		expired, err := r.CreateRecoveryToken(ctx, alice.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE linkding_recovery_token SET expires_at = `+r.marker(1)+` WHERE token_hash = `+r.marker(2), time.Now().UTC().Add(-time.Second), recoveryTokenHash(expired)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ConsumeRecoveryToken(ctx, expired); !errors.Is(err, ErrInvalidRecoveryToken) {
			t.Fatalf("expired token accepted: %v", err)
		}
		inactive, err := r.CreateRecoveryToken(ctx, bob.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE auth_user SET is_active = `+r.marker(1)+` WHERE id = `+r.marker(2), false, bob.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ConsumeRecoveryToken(ctx, inactive); !errors.Is(err, ErrInvalidRecoveryToken) {
			t.Fatalf("token of an inactive user accepted: %v", err)
		}
		if _, err := r.ActiveUserByUsername(ctx, bob.Username); err == nil {
			t.Fatal("inactive user returned for recovery")
		}
	})

	t.Run("deleting the user removes passkey data", func(t *testing.T) {
		carol, err := r.CreateUser(ctx, NewUser{Username: fmt.Sprintf("carol_%d", suffix), Password: "secret-password"})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.AddPasskey(ctx, carol.ID, "Phone", webauthn.Credential{ID: []byte(fmt.Sprintf("cred-c-%d", suffix))}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.CreateRecoveryToken(ctx, carol.ID, time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SaveWebAuthnChallenge(ctx, "register", carol.ID, []byte(`{}`), time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{`DELETE FROM bookmarks_userprofile WHERE user_id = `, `DELETE FROM auth_user WHERE id = `} {
			if _, err := db.ExecContext(ctx, query+r.marker(1), carol.ID); err != nil {
				t.Fatal(err)
			}
		}
		for _, table := range []string{"linkding_passkey", "linkding_recovery_token", "linkding_webauthn_challenge"} {
			var count int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id = `+r.marker(1), carol.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s rows after user deletion: %d err=%v", table, count, err)
			}
		}
	})
}
