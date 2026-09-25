package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestFullBacksUpLiveWALDatabaseAndNestedFiles(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := config.Config{DBEngine: "sqlite", DataDir: dataDir}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "backup-user", Password: "backup-password"})
	if err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(filepath.Join(dataDir, "db.sqlite3-wal"))
	if err != nil {
		t.Fatalf("stat live WAL file: %v", err)
	}
	if walInfo.Size() == 0 {
		t.Fatal("live WAL file is empty")
	}

	files := map[string][]byte{
		"assets/nested/deep/asset.bin":      {0, 1, 2, 255},
		"favicons/example.org/icon.ico":     {9, 8, 7, 6},
		"previews/2026/09/page-preview.dat": []byte("nested preview bytes\x00"),
	}
	for name, contents := range files {
		path := filepath.Join(dataDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o640); err != nil {
			t.Fatal(err)
		}
	}

	backupPath := filepath.Join(t.TempDir(), "full.zip")
	if err := Full(ctx, cfg, backupPath, io.Discard); err != nil {
		t.Fatalf("Full: %v", err)
	}
	archive, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	entries := make(map[string][]byte, len(archive.File))
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatalf("open %q: %v", entry.Name, err)
		}
		contents, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatalf("read %q: %v", entry.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %q: %v", entry.Name, closeErr)
		}
		entries[entry.Name] = contents
	}
	if _, ok := entries["db.sqlite3"]; !ok {
		t.Fatal("archive is missing db.sqlite3")
	}
	for name, want := range files {
		got, ok := entries[name]
		if !ok {
			t.Errorf("archive is missing nested entry %q", name)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("archive entry %q contents = %v, want %v", name, got, want)
		}
	}

	selfBackupPath := filepath.Join(dataDir, "assets", "inside.zip")
	if err := Full(ctx, cfg, selfBackupPath, io.Discard); err != nil {
		t.Fatalf("Full with destination inside assets: %v", err)
	}
	selfArchive, err := zip.OpenReader(selfBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range selfArchive.File {
		if entry.Name == "assets/inside.zip" {
			selfArchive.Close()
			t.Fatal("backup archive contains itself")
		}
	}
	if err := selfArchive.Close(); err != nil {
		t.Fatal(err)
	}

	// Extract the archive into a fresh data directory as the documented restore flow does.
	restoredDir := t.TempDir()
	for _, entry := range archive.File {
		destination := filepath.Join(restoredDir, filepath.FromSlash(entry.Name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			t.Fatal(err)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			_ = reader.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, reader)
		closeOutputErr := output.Close()
		closeReaderErr := reader.Close()
		if copyErr != nil {
			t.Fatal(copyErr)
		}
		if closeOutputErr != nil {
			t.Fatal(closeOutputErr)
		}
		if closeReaderErr != nil {
			t.Fatal(closeReaderErr)
		}
	}
	restored, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: restoredDir})
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer restored.Close()
	var restoredUserID int64
	if err := restored.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username = ?`, "backup-user").Scan(&restoredUserID); err != nil {
		t.Fatalf("query restored user: %v", err)
	}
	if restoredUserID != user.ID {
		t.Fatalf("restored user id = %d, want %d", restoredUserID, user.ID)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(restoredDir, filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("read restored %q: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("restored %q contents = %v, want %v", name, got, want)
		}
	}
}

func TestFullSkipsMissingDataFolders(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "full.zip")
	if err := Full(ctx, cfg, path, io.Discard); err != nil {
		t.Fatalf("Full with missing data folders: %v", err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 1 || archive.File[0].Name != "db.sqlite3" {
		t.Fatalf("archive entries = %v, want only db.sqlite3", zipEntryNames(archive.File))
	}
}

func TestBackupRejectsNonSQLiteEngine(t *testing.T) {
	cfg := config.Config{DBEngine: "postgres", DataDir: t.TempDir()}
	for name, run := range map[string]func() error{
		"full": func() error {
			return Full(context.Background(), cfg, filepath.Join(t.TempDir(), "full.zip"), io.Discard)
		},
		"database": func() error {
			return Database(context.Background(), cfg, filepath.Join(t.TempDir(), "db.sqlite3"), io.Discard)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("backup unexpectedly accepted PostgreSQL engine")
			}
		})
	}
}

func TestBackupRejectsSourceDatabaseAsDestinationWithoutChangingIt(t *testing.T) {
	ctx := context.Background()
	for name, run := range map[string]func(context.Context, config.Config, string, io.Writer) error{
		"full":     Full,
		"database": Database,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
			db, err := store.Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Migrate(ctx, db, "sqlite"); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "protected-source", Password: "backup-password"}); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(cfg.DataDir, "db.sqlite3")
			before, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(ctx, cfg, sourcePath, io.Discard); err == nil {
				t.Fatal("backup accepted source database as destination")
			}
			after, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatalf("source database was removed or replaced: %v", err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("source database changed after rejected backup")
			}
		})
	}
}

func TestDatabaseWritesLegacySQLiteFile(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "legacy-backup-user", Password: "backup-password"})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	if err := Database(ctx, cfg, path, io.Discard); err != nil {
		t.Fatalf("Database: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	magic := make([]byte, 16)
	_, readErr := io.ReadFull(file, magic)
	closeErr := file.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if string(magic) != "SQLite format 3\x00" {
		t.Fatalf("legacy backup header = %q, want SQLite database header", magic)
	}

	restoredDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	var restoredUserID int64
	if err := restoredDB.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username = ?`, "legacy-backup-user").Scan(&restoredUserID); err != nil {
		t.Fatalf("query legacy backup: %v", err)
	}
	if restoredUserID != user.ID {
		t.Fatalf("legacy backup user id = %d, want %d", restoredUserID, user.ID)
	}
}

func zipEntryNames(entries []*zip.File) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
