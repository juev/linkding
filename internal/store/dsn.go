package store

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/juev/linkding/internal/config"
)

func databaseDSN(cfg config.Config) (driver, dsn string, err error) {
	switch cfg.DBEngine {
	case "sqlite":
		path := filepath.Join(cfg.DataDir, "db.sqlite3")
		if raw, ok := cfg.DBOptions["database"]; ok {
			if err := json.Unmarshal(raw, &path); err != nil || path == "" {
				return "", "", fmt.Errorf("LD_DB_OPTIONS.database: expected path")
			}
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return "", "", fmt.Errorf("SQLite path: %w", err)
		}
		u := &url.URL{Scheme: "file", Path: path}
		query := u.Query()
		query.Add("_pragma", "foreign_keys(1)")
		if _, customTimeout := cfg.DBOptions["timeout"]; !customTimeout {
			query.Add("_pragma", "busy_timeout(5000)")
		}
		for key, raw := range cfg.DBOptions {
			switch key {
			case "database":
			case "timeout":
				var seconds float64
				if err := json.Unmarshal(raw, &seconds); err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 86400 {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.timeout: expected seconds in [0, 86400]")
				}
				query.Set("_busy_timeout", fmt.Sprintf("%.0f", seconds*1000))
			case "transaction_mode":
				var mode string
				if string(raw) == "null" {
					continue
				}
				if err := json.Unmarshal(raw, &mode); err != nil {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.transaction_mode: expected DEFERRED, IMMEDIATE, or EXCLUSIVE")
				}
				mode = strings.ToLower(mode)
				if mode != "deferred" && mode != "immediate" && mode != "exclusive" {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.transaction_mode: expected DEFERRED, IMMEDIATE, or EXCLUSIVE")
				}
				query.Set("_txlock", mode)
			case "init_command":
				var commands string
				if err := json.Unmarshal(raw, &commands); err != nil {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.init_command: expected SQL string")
				}
				for _, command := range strings.Split(commands, ";") {
					if command = strings.TrimSpace(command); command != "" {
						query.Add("_ld_init", command)
					}
				}
			case "check_same_thread", "uri", "detect_types", "cached_statements":
				// Python driver settings. Go's database/sql handles connection safety.
			case "isolation_level":
				if string(raw) != "null" && string(raw) != `""` {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.isolation_level is unsupported")
				}
			case "autocommit":
				if string(raw) != "true" {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.autocommit is unsupported")
				}
			default:
				return "", "", fmt.Errorf("unsupported SQLite LD_DB_OPTIONS.%s", key)
			}
		}
		u.RawQuery = query.Encode()
		return "sqlite", u.String(), nil
	case "postgres":
		u := &url.URL{Scheme: "postgres", User: url.UserPassword(cfg.DBUser, cfg.DBPassword), Host: cfg.DBHost, Path: "/" + cfg.DBDatabase}
		if cfg.DBPort != "" {
			u.Host = net.JoinHostPort(cfg.DBHost, cfg.DBPort)
		}
		query := u.Query()
		for key, raw := range cfg.DBOptions {
			switch key {
			case "sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "application_name", "options", "target_session_attrs", "channel_binding", "service":
				var value any
				if err := json.Unmarshal(raw, &value); err != nil || value == nil {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.%s: expected scalar", key)
				}
				switch value := value.(type) {
				case string:
					query.Set(key, value)
				case float64, bool:
					query.Set(key, fmt.Sprint(value))
				default:
					return "", "", fmt.Errorf("LD_DB_OPTIONS.%s: expected scalar", key)
				}
			case "assume_role":
				var role string
				if err := json.Unmarshal(raw, &role); err != nil || role == "" {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.assume_role: expected role name")
				}
			case "isolation_level":
				if string(raw) == "null" {
					continue
				}
				var level int
				if err := json.Unmarshal(raw, &level); err != nil || level < 1 || level > 4 {
					return "", "", fmt.Errorf("LD_DB_OPTIONS.isolation_level: expected psycopg level 1 to 4")
				}
			default:
				return "", "", fmt.Errorf("unsupported PostgreSQL LD_DB_OPTIONS.%s", key)
			}
		}
		u.RawQuery = query.Encode()
		return "pgx", u.String(), nil
	default:
		return "", "", fmt.Errorf("unsupported database engine %q", cfg.DBEngine)
	}
}
