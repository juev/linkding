package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*/*.sql
var migrationFiles embed.FS

// Migrate applies the schema for a new Go installation. The initial SQLite
// migration mirrors a fresh linkding v1.47.0 database for data import.
func Migrate(ctx context.Context, db *sql.DB, engine string) error {
	return migrate(ctx, db, engine, false)
}

// MigrateImported applies only Go-specific migrations to a database that
// already has the pinned upstream schema and data.
func MigrateImported(ctx context.Context, db *sql.DB, engine string) error {
	return migrate(ctx, db, engine, true)
}

func migrate(ctx context.Context, db *sql.DB, engine string, imported bool) error {
	var dialect goose.Dialect
	switch engine {
	case "sqlite":
		dialect = goose.DialectSQLite3
	case "postgres":
		dialect = goose.DialectPostgres
	default:
		return fmt.Errorf("unsupported database engine %q", engine)
	}
	folder, err := fs.Sub(migrationFiles, "migrations/"+engine)
	if err != nil {
		return fmt.Errorf("read %s migrations: %w", engine, err)
	}
	provider, err := goose.NewProvider(dialect, db, folder)
	if err != nil {
		return fmt.Errorf("prepare %s migrations: %w", engine, err)
	}
	if imported {
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return fmt.Errorf("read imported migration version: %w", err)
		}
		if version == 0 {
			query := `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`
			if engine == "postgres" {
				query = `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, 1)`
			}
			if _, err := db.ExecContext(ctx, query, 1); err != nil {
				return fmt.Errorf("record imported upstream schema: %w", err)
			}
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply %s migrations: %w", engine, err)
	}
	if err := seedDjangoMetadata(ctx, db, engine); err != nil {
		return fmt.Errorf("seed Django metadata: %w", err)
	}
	return nil
}
