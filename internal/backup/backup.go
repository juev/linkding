// Package backup creates online SQLite backups in the formats exposed by linkding.
package backup

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

// Full writes a ZIP containing a consistent SQLite snapshot and the data files.
// Nested file paths are retained so the archive can be restored directly.
func Full(ctx context.Context, cfg config.Config, destination string, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	if err := checkDestination(cfg, destination); err != nil {
		return err
	}
	fmt.Fprintln(output, "Create database backup...")
	snapshot, err := sqliteSnapshot(ctx, cfg)
	if err != nil {
		return err
	}
	defer os.Remove(snapshot)

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".linkding-full-backup-*.zip")
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	defer temporary.Close()
	archive := zip.NewWriter(temporary)
	if err := addFile(archive, snapshot, "db.sqlite3"); err != nil {
		return err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	temporaryPathAbs, err := filepath.Abs(temporaryPath)
	if err != nil {
		return err
	}
	for _, folder := range []string{"assets", "favicons", "previews"} {
		root := filepath.Join(cfg.DataDir, folder)
		info, err := os.Stat(root)
		if os.IsNotExist(err) {
			fmt.Fprintf(output, "No %s folder found. Skipping...\n", folder)
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s folder: %w", folder, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", root)
		}
		fmt.Fprintf(output, "Backup bookmark %s...\n", folder)
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root || entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("backup file is a symlink: %s", path)
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if absolute == destinationPath || absolute == temporaryPathAbs {
				return nil
			}
			relative, err := filepath.Rel(cfg.DataDir, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return fmt.Errorf("file outside data directory: %s", path)
			}
			return addFile(archive, path, filepath.ToSlash(relative))
		}); err != nil {
			return fmt.Errorf("archive %s: %w", folder, err)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync backup archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close backup archive: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish backup archive: %w", err)
	}
	fmt.Fprintf(output, "Backup created at %s\n", destination)
	return nil
}

// Database writes the legacy database-only backup format.
func Database(ctx context.Context, cfg config.Config, destination string, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	if err := checkDestination(cfg, destination); err != nil {
		return err
	}
	snapshot, err := sqliteSnapshot(ctx, cfg)
	if err != nil {
		return err
	}
	defer os.Remove(snapshot)
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".linkding-backup-*.sqlite3")
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	source, err := os.Open(snapshot)
	if err != nil {
		temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, source)
	closeSourceErr := source.Close()
	if copyErr != nil || closeSourceErr != nil {
		temporary.Close()
		return fmt.Errorf("copy database backup: %w", firstError(copyErr, closeSourceErr))
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish database backup: %w", err)
	}
	fmt.Fprintf(output, "Backup created at %s\nThis backup method is deprecated. Use full_backup to include the data files.\n", destination)
	return nil
}

func checkDestination(cfg config.Config, destination string) error {
	sourcePath, err := filepath.Abs(filepath.Join(cfg.DataDir, "db.sqlite3"))
	if err != nil {
		return err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if destinationPath == sourcePath {
		return fmt.Errorf("backup destination must differ from the source database")
	}
	return nil
}

func sqliteSnapshot(ctx context.Context, cfg config.Config) (string, error) {
	if cfg.DBEngine != "sqlite" {
		return "", fmt.Errorf("SQLite backup is unavailable for database engine %q", cfg.DBEngine)
	}
	source := filepath.Join(cfg.DataDir, "db.sqlite3")
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("open SQLite database for backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("SQLite database is not a regular file: %s", source)
	}
	// Upstream's backup commands read data/db.sqlite3, independent of DB_OPTIONS.
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: cfg.DataDir})
	if err != nil {
		return "", err
	}
	defer db.Close()
	temporary, err := os.CreateTemp("", "linkding-snapshot-*.sqlite3")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("snapshot SQLite database: %w", err)
	}
	return path, nil
}

func addFile(archive *zip.Writer, path, name string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup file is not regular: %s", path)
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = name
	header.Method = zip.Deflate
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	if _, err := io.Copy(entry, file); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
