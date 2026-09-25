package main

import (
	"context"
	"strings"
	"testing"
)

func TestEnableWALMatchesBootstrap(t *testing.T) {
	ctx := context.Background()
	db := openBootstrapDB(t)
	for range 2 {
		if err := enableWAL(ctx, db, "sqlite"); err != nil {
			t.Fatal(err)
		}
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("SQLite journal mode = %q, want WAL", mode)
	}
	if err := enableWAL(ctx, nil, "postgres"); err != nil {
		t.Fatalf("PostgreSQL WAL command should be a no-op: %v", err)
	}
}
