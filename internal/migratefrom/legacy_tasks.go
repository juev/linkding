package migratefrom

import (
	"context"
	"database/sql"
	"fmt"
)

// CountLegacyTasks checks the django-background-tasks table left by older
// installations. Its tasks must be migrated by Python before Go takes over.
func CountLegacyTasks(ctx context.Context, db *sql.DB, engine string) (count int64, exists bool, err error) {
	var query string
	switch engine {
	case "sqlite":
		query = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='background_task')`
	case "postgres":
		query = `SELECT to_regclass('public.background_task') IS NOT NULL`
	default:
		return 0, false, fmt.Errorf("unsupported database engine %q", engine)
	}
	if err := db.QueryRowContext(ctx, query).Scan(&exists); err != nil {
		return 0, false, fmt.Errorf("inspect legacy background task table: %w", err)
	}
	if !exists {
		return 0, false, nil
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM background_task`).Scan(&count); err != nil {
		return 0, true, fmt.Errorf("count legacy background tasks: %w", err)
	}
	return count, true, nil
}

// LegacyTasksError explains how to clear old queued tasks before cutover.
func LegacyTasksError(count int64) error {
	return fmt.Errorf("%d legacy background tasks remain; run Python linkding v1.47.0 migrate_tasks and drain Huey before starting Go", count)
}
