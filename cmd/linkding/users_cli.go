package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func runEnsureSuperuser(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("ensure_superuser", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	username := flags.String("username", "", "admin username")
	email := flags.String("email", "", "admin email")
	password := flags.String("password", "", "admin password")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("usage: linkding ensure_superuser --username <name> [--email <address>] [--password <password>]")
	}
	if *username == "" {
		return fmt.Errorf("superuser username is required")
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
	return ensureSuperuser(ctx, db, cfg.DBEngine, *username, *email, *password)
}

func runCreateInitialSuperuser(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: linkding create_initial_superuser")
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
	return ensureInitialSuperuser(ctx, db, cfg)
}
