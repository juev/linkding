package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/config"
)

func TestSQLiteDatabaseOptionsApplyPerConnection(t *testing.T) {
	cfg := config.Config{
		DBEngine: "sqlite",
		DataDir:  filepath.Join(t.TempDir(), "data"),
		DBOptions: map[string]json.RawMessage{
			"timeout":          json.RawMessage(`1.25`),
			"transaction_mode": json.RawMessage(`"IMMEDIATE"`),
			"init_command":     json.RawMessage(`"PRAGMA cache_size=1234; CREATE TEMP TABLE init_marker (id integer)"`),
		},
	}
	_, dsn, err := databaseDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("_txlock") != "immediate" || u.Query().Get("_busy_timeout") != "1250" {
		t.Fatalf("SQLite DSN options: %s", u.RawQuery)
	}
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var timeout, cacheSize int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA cache_size").Scan(&cacheSize); err != nil {
		t.Fatal(err)
	}
	if timeout != 1250 || cacheSize != 1234 {
		t.Fatalf("SQLite options: busy_timeout=%d cache_size=%d", timeout, cacheSize)
	}
	ctx := context.Background()
	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*sql.Conn{first, second} {
		var count int
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM init_marker").Scan(&count); err != nil {
			t.Fatalf("init command missing on connection: %v", err)
		}
	}
}

func TestSQLiteTransactionModeDefaultsToImmediate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options map[string]json.RawMessage
		want    string
	}{
		{name: "default", want: "immediate"},
		{name: "explicit deferred", options: map[string]json.RawMessage{"transaction_mode": json.RawMessage(`"DEFERRED"`)}, want: "deferred"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, dsn, err := databaseDSN(config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DBOptions: tc.options})
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.Query().Get("_txlock"); got != tc.want {
				t.Fatalf("SQLite transaction mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostgresDatabaseOptionsEnterConnectionString(t *testing.T) {
	cfg := config.Config{
		DBEngine:   "postgres",
		DBHost:     "::1",
		DBPort:     "5432",
		DBDatabase: "linkding",
		DBUser:     "u",
		DBPassword: "p",
		DBOptions: map[string]json.RawMessage{
			"sslmode":         json.RawMessage(`"require"`),
			"connect_timeout": json.RawMessage(`5`),
		},
	}
	driver, dsn, err := databaseDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if driver != "pgx" || u.Host != "[::1]:5432" || u.Query().Get("sslmode") != "require" || u.Query().Get("connect_timeout") != "5" {
		t.Fatalf("Postgres DSN: %s", dsn)
	}
}

func TestUnknownDatabaseOptionFailsBeforeConnecting(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		cfg := config.Config{DBEngine: engine, DataDir: t.TempDir(), DBOptions: map[string]json.RawMessage{"unsupported": json.RawMessage(`true`)}}
		if _, err := Open(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("%s unsupported option: %v", engine, err)
		}
	}
}

func TestPostgresRoleAndIsolationOptionsApplyPerConnection(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	cfg := config.Config{
		DBEngine:   "postgres",
		DBHost:     u.Hostname(),
		DBPort:     u.Port(),
		DBDatabase: strings.TrimPrefix(u.Path, "/"),
		DBUser:     u.User.Username(),
		DBPassword: password,
		DBOptions: map[string]json.RawMessage{
			"sslmode":         json.RawMessage(`"disable"`),
			"assume_role":     json.RawMessage(`"postgres"`),
			"isolation_level": json.RawMessage(`4`),
		},
	}
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role, isolation string
	if err := db.QueryRow(`SELECT current_user, current_setting('default_transaction_isolation')`).Scan(&role, &isolation); err != nil {
		t.Fatal(err)
	}
	if role != "postgres" || isolation != "serializable" {
		t.Fatalf("PostgreSQL options: role=%q isolation=%q", role, isolation)
	}
}
