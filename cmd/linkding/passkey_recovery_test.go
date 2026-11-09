package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func TestPasskeyRecoveryLink(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "secret-password"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DBEngine: "sqlite", ContextPath: "linkding/"}
	link, err := createPasskeyRecoveryLink(ctx, db, cfg, "alice", "https://links.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "links.example.com" || parsed.Path != "/linkding/passkeys/recover" {
		t.Fatalf("recovery link %q: %v", link, err)
	}
	signedIn, err := users.ConsumeRecoveryToken(ctx, parsed.Query().Get("token"))
	if err != nil || signedIn.ID != user.ID {
		t.Fatalf("link token: user=%+v err=%v", signedIn, err)
	}
	relative, err := createPasskeyRecoveryLink(ctx, db, config.Config{DBEngine: "sqlite"}, "alice", "")
	if err != nil || !strings.HasPrefix(relative, "/passkeys/recover?token=") {
		t.Fatalf("link without base URL: %q err=%v", relative, err)
	}
	if _, err := createPasskeyRecoveryLink(ctx, db, cfg, "nobody", ""); err == nil {
		t.Fatal("link created for an unknown user")
	}
}

func TestRunPasskeyRecoveryValidatesArguments(t *testing.T) {
	// Every case fails during argument validation, before the database opens.
	for _, args := range [][]string{nil, {"--username", ""}, {"--username", "alice", "extra"}, {"--username", "alice", "--base-url", "links.example.com"}} {
		var out bytes.Buffer
		if err := runPasskeyRecovery(context.Background(), args, &out); err == nil {
			t.Fatalf("arguments %q accepted", args)
		}
	}
}

func TestWarnUsersWithoutPasskey(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "secret-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "secret-password"}); err != nil {
		t.Fatal(err)
	}
	if err := users.AddPasskey(ctx, alice.ID, "Laptop", webauthn.Credential{ID: []byte("alice-key")}); err != nil {
		t.Fatal(err)
	}
	var messages []string
	logf := func(format string, args ...any) { messages = append(messages, fmt.Sprintf(format, args...)) }
	if err := warnUsersWithoutPasskey(ctx, db, config.Config{DBEngine: "sqlite"}, logf); err != nil || len(messages) != 0 {
		t.Fatalf("warning with password sign-in enabled: %q err=%v", messages, err)
	}
	if err := warnUsersWithoutPasskey(ctx, db, config.Config{DBEngine: "sqlite", DisablePasswordLogin: true}, logf); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "1 active user(s) have no passkey") || !strings.Contains(messages[0], "passkey_recovery") {
		t.Fatalf("warning: %q", messages)
	}
}
