package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/juev/linkding/internal/config"
	_ "modernc.org/sqlite"
)

// Open connects to the configured database. Schema creation is handled by migrations.
func Open(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	driver, dsn, err := databaseDSN(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.DBEngine == "sqlite" {
		if err := registerSQLiteUnicode(); err != nil {
			return nil, fmt.Errorf("register SQLite Unicode functions: %w", err)
		}
		registerSQLiteInitCommands()
		if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	var db *sql.DB
	if cfg.DBEngine == "postgres" {
		pgConfig, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("PostgreSQL connection options: %w", err)
		}
		var role string
		var isolation int
		if raw, ok := cfg.DBOptions["assume_role"]; ok {
			if err := json.Unmarshal(raw, &role); err != nil {
				return nil, fmt.Errorf("LD_DB_OPTIONS.assume_role: %w", err)
			}
		}
		if raw, ok := cfg.DBOptions["isolation_level"]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &isolation); err != nil {
				return nil, fmt.Errorf("LD_DB_OPTIONS.isolation_level: %w", err)
			}
		}
		levels := []string{"", "READ UNCOMMITTED", "READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"}
		db = sql.OpenDB(stdlib.GetConnector(*pgConfig, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			if isolation != 0 {
				if _, err := conn.Exec(ctx, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+levels[isolation]); err != nil {
					return err
				}
			}
			if role != "" {
				if _, err := conn.Exec(ctx, "SET ROLE "+(pgx.Identifier{role}).Sanitize()); err != nil {
					return err
				}
			}
			return nil
		})))
	} else {
		db, err = sql.Open(driver, dsn)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", cfg.DBEngine, err)
		}
	}
	if cfg.DBEngine == "sqlite" {
		// A bounded pool avoids unbounded per-connection SQLite page caches.
		db.SetMaxOpenConns(4)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", cfg.DBEngine, err)
	}
	return db, nil
}
