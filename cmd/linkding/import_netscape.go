package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/importexport"
	"github.com/juev/linkding/internal/store"
)

func runImportNetscape(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		return fmt.Errorf("usage: linkding import_netscape <file> <user>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return importNetscape(ctx, cfg, args[0], args[1])
}

func importNetscape(ctx context.Context, cfg config.Config, filename, username string) error {
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read Netscape bookmark file %q: %w", filename, err)
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("read Netscape bookmark file %q: file is not valid UTF-8", filename)
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		return err
	}
	var ownerID int64
	marker := "?"
	if cfg.DBEngine == "postgres" {
		marker = "$1"
	}
	err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username = `+marker, username).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("user %q does not exist", username)
	}
	if err != nil {
		return fmt.Errorf("find user %q: %w", username, err)
	}
	if _, err := importexport.ImportNetscape(ctx, db, cfg, ownerID, string(content), importexport.ImportOptions{}); err != nil {
		return fmt.Errorf("import Netscape bookmarks for user %q: %w", username, err)
	}
	return nil
}
