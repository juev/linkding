package store

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"modernc.org/sqlite"
)

var sqliteInitRegistration sync.Once

// The pinned Django backend runs LD_DB_OPTIONS.init_command on every new
// SQLite connection after enabling foreign keys. The driver hook provides the
// same timing for arbitrary administrator-supplied SQL statements.
func registerSQLiteInitCommands() {
	sqliteInitRegistration.Do(func() {
		sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
			u, err := url.Parse(dsn)
			if err != nil {
				return fmt.Errorf("parse SQLite DSN: %w", err)
			}
			for _, command := range u.Query()["_ld_init"] {
				if _, err := conn.ExecContext(context.Background(), command, nil); err != nil {
					return fmt.Errorf("SQLite init command: %w", err)
				}
			}
			return nil
		})
	})
}
