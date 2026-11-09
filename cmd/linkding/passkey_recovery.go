package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

const passkeyRecoveryTTL = 15 * time.Minute

// runPasskeyRecovery prints a single-use sign-in link for a user who lost
// every passkey. Access to this command already implies access to the
// database, so it adds no new way into an account.
func runPasskeyRecovery(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("passkey_recovery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	username := flags.String("username", "", "user to sign in")
	baseURL := flags.String("base-url", "", "public scheme and host, for example https://links.example.com")
	usage := fmt.Errorf("usage: linkding passkey_recovery --username <name> [--base-url <https://host>]")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *username == "" {
		return usage
	}
	if *baseURL != "" {
		parsed, err := url.Parse(*baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("--base-url must be an http or https URL such as https://links.example.com")
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		return err
	}
	link, err := createPasskeyRecoveryLink(ctx, db, cfg, *username, *baseURL)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Single-use sign-in link for %s, valid for %d minutes:\n%s\n", *username, int(passkeyRecoveryTTL/time.Minute), link)
	return err
}

func createPasskeyRecoveryLink(ctx context.Context, db *sql.DB, cfg config.Config, username, baseURL string) (string, error) {
	users := auth.NewRepository(db, cfg.DBEngine)
	user, err := users.ActiveUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	token, err := users.CreateRecoveryToken(ctx, user.ID, passkeyRecoveryTTL)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseURL, "/") + cfg.URLPrefix() + "passkeys/recover?token=" + url.QueryEscape(token), nil
}

// warnUsersWithoutPasskey reports, when password sign-in is disabled, how many
// active users cannot sign in with a passkey.
func warnUsersWithoutPasskey(ctx context.Context, db *sql.DB, cfg config.Config, logf func(string, ...any)) error {
	if !cfg.DisablePasswordLogin {
		return nil
	}
	count, err := auth.NewRepository(db, cfg.DBEngine).CountActiveUsersWithoutPasskey(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		logf("warning: password sign-in is disabled and %d active user(s) have no passkey; they can sign in only through OIDC, the auth proxy, or a link from `linkding passkey_recovery --username <name>`", count)
	}
	return nil
}
