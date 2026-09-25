package migratefrom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"

	_ "modernc.org/sqlite"
)

const upstreamBookmarkMigration = "0054_bookmarkbundle_filter_shared_and_more"

type SQLitePreflight struct {
	SourceDir      string
	BookmarkCount  int64
	UserCount      int64
	AssetCount     int64
	PendingTasks   int64
	ScheduledTasks int64
}

type SQLiteMigration struct {
	SQLitePreflight
	TargetDir   string
	CopiedFiles int
}

// MigrateSQLite requires a stopped Python server and a target directory that
// does not exist yet. It leaves the source untouched and verifies every table
// row count and copied file before returning.
func MigrateSQLite(ctx context.Context, sourceDir, targetDir string) (SQLiteMigration, error) {
	report, err := InspectSQLite(ctx, sourceDir)
	if err != nil {
		return SQLiteMigration{}, err
	}
	if report.PendingTasks != 0 || report.ScheduledTasks != 0 {
		return SQLiteMigration{}, fmt.Errorf("Huey queue is not empty: %d tasks, %d scheduled; drain it before migration", report.PendingTasks, report.ScheduledTasks)
	}
	target, err := filepath.Abs(targetDir)
	if err != nil {
		return SQLiteMigration{}, err
	}
	if target == report.SourceDir {
		return SQLiteMigration{}, fmt.Errorf("target data directory must differ from source")
	}
	if relative, err := filepath.Rel(report.SourceDir, target); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return SQLiteMigration{}, fmt.Errorf("target data directory cannot be inside source")
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return SQLiteMigration{}, fmt.Errorf("create new target data directory: %w", err)
	}
	result := SQLiteMigration{SQLitePreflight: report, TargetDir: target}
	sourceDB, err := openSQLiteReadOnly(filepath.Join(report.SourceDir, "db.sqlite3"))
	if err != nil {
		return result, err
	}
	defer sourceDB.Close()
	targetDBPath := filepath.Join(target, "db.sqlite3")
	if _, err := sourceDB.ExecContext(ctx, `VACUUM INTO ?`, targetDBPath); err != nil {
		return result, fmt.Errorf("copy source SQLite database: %w", err)
	}
	result.CopiedFiles, err = copyDataFiles(report.SourceDir, target)
	if err != nil {
		return result, err
	}
	targetDB, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: target})
	if err != nil {
		return result, err
	}
	defer targetDB.Close()
	before, err := sqliteTableCounts(ctx, sourceDB)
	if err != nil {
		return result, err
	}
	after, err := sqliteTableCounts(ctx, targetDB)
	if err != nil {
		return result, err
	}
	for name, count := range before {
		if after[name] != count {
			return result, fmt.Errorf("copied table %s has %d rows, source has %d", name, after[name], count)
		}
	}
	if err := store.MigrateImported(ctx, targetDB, "sqlite"); err != nil {
		return result, err
	}
	var integrity string
	if err := targetDB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return result, fmt.Errorf("target SQLite integrity_check failed: %s: %w", integrity, err)
	}
	return result, nil
}

func sqliteTableCounts(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(names))
	for _, name := range names {
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+quoted).Scan(&count); err != nil {
			return nil, err
		}
		counts[name] = count
	}
	return counts, nil
}

func copyDataFiles(source, target string) (int, error) {
	copied := 0
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source data contains symlink %s", relative)
		}
		if entry.IsDir() {
			return os.Mkdir(filepath.Join(target, relative), 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source data contains unsupported file %s", relative)
		}
		if filepath.Dir(relative) == "." && (relative == "db.sqlite3" || strings.HasPrefix(relative, "db.sqlite3-") || relative == "tasks.sqlite3" || strings.HasPrefix(relative, "tasks.sqlite3-")) {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(filepath.Join(target, relative), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(io.MultiWriter(output, hash), input)
		closeErr := output.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		copiedFile, err := os.Open(filepath.Join(target, relative))
		if err != nil {
			return err
		}
		copiedHash := sha256.New()
		_, err = io.Copy(copiedHash, copiedFile)
		closeErr = copiedFile.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if !bytes.Equal(hash.Sum(nil), copiedHash.Sum(nil)) {
			return fmt.Errorf("copied file checksum mismatch: %s", relative)
		}
		copied++
		return nil
	})
	return copied, err
}

// InspectSQLite reads an installed v1.47.0 data directory without changing it.
// The Python server must be stopped before the returned preflight is used to copy data.
func InspectSQLite(ctx context.Context, sourceDir string) (SQLitePreflight, error) {
	abs, err := filepath.Abs(sourceDir)
	if err != nil {
		return SQLitePreflight{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return SQLitePreflight{}, fmt.Errorf("source data directory is unavailable: %s", abs)
	}
	db, err := openSQLiteReadOnly(filepath.Join(abs, "db.sqlite3"))
	if err != nil {
		return SQLitePreflight{}, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return SQLitePreflight{}, fmt.Errorf("open source SQLite database: %w", err)
	}
	var migration string
	if err := db.QueryRowContext(ctx, `SELECT name FROM django_migrations WHERE app = 'bookmarks' ORDER BY id DESC LIMIT 1`).Scan(&migration); err != nil || migration != upstreamBookmarkMigration {
		return SQLitePreflight{}, fmt.Errorf("source bookmarks schema must be linkding v1.47.0 (%s); update Python linkding first", upstreamBookmarkMigration)
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return SQLitePreflight{}, fmt.Errorf("source SQLite quick_check failed: %s: %w", integrity, err)
	}
	report := SQLitePreflight{SourceDir: abs}
	for _, check := range []struct {
		query string
		count *int64
	}{
		{`SELECT count(*) FROM bookmarks_bookmark`, &report.BookmarkCount},
		{`SELECT count(*) FROM auth_user`, &report.UserCount},
		{`SELECT count(*) FROM bookmarks_bookmarkasset`, &report.AssetCount},
	} {
		if err := db.QueryRowContext(ctx, check.query).Scan(check.count); err != nil {
			return SQLitePreflight{}, fmt.Errorf("inspect source table: %w", err)
		}
	}
	if err := verifyReferencedFiles(ctx, db, abs); err != nil {
		return SQLitePreflight{}, err
	}
	report.PendingTasks, report.ScheduledTasks, err = inspectHueyQueue(ctx, abs)
	if err != nil {
		return SQLitePreflight{}, err
	}
	return report, nil
}

func inspectHueyQueue(ctx context.Context, sourceDir string) (int64, int64, error) {
	queuePath := filepath.Join(sourceDir, "tasks.sqlite3")
	if _, err := os.Stat(queuePath); os.IsNotExist(err) {
		return 0, 0, nil
	} else if err != nil {
		return 0, 0, fmt.Errorf("inspect Huey queue file: %w", err)
	}
	queue, err := openSQLiteReadOnly(queuePath)
	if err != nil {
		return 0, 0, err
	}
	defer queue.Close()
	var pending, scheduled int64
	if err := queue.QueryRowContext(ctx, `SELECT count(*) FROM task`).Scan(&pending); err != nil {
		return 0, 0, fmt.Errorf("inspect Huey task queue: %w", err)
	}
	if err := queue.QueryRowContext(ctx, `SELECT count(*) FROM schedule`).Scan(&scheduled); err != nil {
		return 0, 0, fmt.Errorf("inspect Huey scheduled tasks: %w", err)
	}
	return pending, scheduled, nil
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	uri := &url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite %s: %w", path, err)
	}
	return db, nil
}
