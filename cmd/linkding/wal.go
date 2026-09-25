package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func enableWAL(ctx context.Context, db *sql.DB, engine string) error {
	if engine != "sqlite" {
		return nil
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=wal").Scan(&mode); err != nil {
		return fmt.Errorf("enable SQLite WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("enable SQLite WAL: journal mode is %q", mode)
	}
	return nil
}

func runEnableWAL(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: linkding enable_wal")
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
	return enableWAL(ctx, db, cfg.DBEngine)
}
