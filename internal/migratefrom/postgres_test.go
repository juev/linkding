package migratefrom

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratePinnedUpstreamPostgresFixture(t *testing.T) {
	sourceDSN := os.Getenv("LINKDING_TEST_SOURCE_POSTGRES_DSN")
	targetDSN := os.Getenv("LINKDING_TEST_TARGET_POSTGRES_DSN")
	sourceDir := os.Getenv("LINKDING_TEST_SOURCE_POSTGRES_DATA")
	if sourceDSN == "" || targetDSN == "" || sourceDir == "" {
		t.Skip("set source/target PostgreSQL DSNs and stopped upstream data directory copy")
	}
	ctx := context.Background()
	report, err := MigratePostgres(ctx, sourceDSN, targetDSN, sourceDir, filepath.Join(t.TempDir(), "migrated"))
	if err != nil {
		t.Fatal(err)
	}
	if report.BookmarkCount != 1 || report.UserCount != 1 || report.AssetCount != 1 || report.CopiedFiles < 2 {
		t.Fatalf("fixture counts: %+v", report)
	}
	assetPath := filepath.Join("assets", "pg_fixture.txt.gz")
	before, err := os.ReadFile(filepath.Join(sourceDir, assetPath))
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(report.TargetDir, assetPath))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("migrated asset differs: err=%v", err)
	}
	target, err := sql.Open("pgx", targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var tags, tokens, feeds int
	if err := target.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM bookmarks_bookmark_tags),
		(SELECT count(*) FROM bookmarks_apitoken), (SELECT count(*) FROM bookmarks_feedtoken)`).Scan(&tags, &tokens, &feeds); err != nil {
		t.Fatal(err)
	}
	if tags != 1 || tokens != 1 || feeds != 1 {
		t.Fatalf("relationships and tokens: tags=%d api=%d feeds=%d", tags, tokens, feeds)
	}
	verifyPinnedPostgresValues(t, sourceDSN, targetDSN)
}

func TestVerifyPinnedPostgresMigrationFixture(t *testing.T) {
	sourceDSN := os.Getenv("LINKDING_TEST_SOURCE_POSTGRES_DSN")
	targetDSN := os.Getenv("LINKDING_TEST_TARGET_POSTGRES_DSN")
	if sourceDSN == "" || targetDSN == "" {
		t.Skip("set source and target PostgreSQL DSNs")
	}
	verifyPinnedPostgresValues(t, sourceDSN, targetDSN)
}

func TestMigratePostgresRejectsPopulatedTargetBeforeWriting(t *testing.T) {
	sourceDSN := os.Getenv("LINKDING_TEST_SOURCE_POSTGRES_DSN")
	sourceDir := os.Getenv("LINKDING_TEST_SOURCE_POSTGRES_DATA")
	if sourceDSN == "" || sourceDir == "" {
		t.Skip("set source PostgreSQL DSN and data directory")
	}
	targetDir := filepath.Join(t.TempDir(), "target")
	if _, err := MigratePostgres(context.Background(), sourceDSN, sourceDSN, sourceDir, targetDir); err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("populated target was accepted: %v", err)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("target directory was created: %v", err)
	}
}

func verifyPinnedPostgresValues(t *testing.T, sourceDSN, targetDSN string) {
	t.Helper()
	ctx := context.Background()
	source, err := sql.Open("pgx", sourceDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := sql.Open("pgx", targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, item := range []struct {
		name, query string
	}{
		{"password hash", `SELECT password FROM auth_user WHERE username='pgparity'`},
		{"API token", `SELECT key FROM bookmarks_apitoken ORDER BY id LIMIT 1`},
		{"feed token", `SELECT key FROM bookmarks_feedtoken ORDER BY key LIMIT 1`},
		{"bookmark title", `SELECT title FROM bookmarks_bookmark WHERE url='https://postgres-fixture.example/item'`},
		{"tag relationship", `SELECT name FROM bookmarks_tag WHERE id=(SELECT tag_id FROM bookmarks_bookmark_tags LIMIT 1)`},
		{"asset metadata", `SELECT file || ':' || file_size::text || ':' || status FROM bookmarks_bookmarkasset ORDER BY id LIMIT 1`},
	} {
		var before, after string
		if err := source.QueryRowContext(ctx, item.query).Scan(&before); err != nil {
			t.Fatalf("read source %s: %v", item.name, err)
		}
		if err := target.QueryRowContext(ctx, item.query).Scan(&after); err != nil {
			t.Fatalf("read target %s: %v", item.name, err)
		}
		if before != after {
			t.Errorf("migrated %s differs from source", item.name)
		}
	}
	for _, item := range []struct {
		table, sequence string
	}{
		{"auth_user", "auth_user_id_seq"},
		{"bookmarks_bookmark", "bookmarks_bookmark_id_seq"},
	} {
		var maximum, lastValue int64
		var isCalled bool
		if err := target.QueryRowContext(ctx, `SELECT max(id) FROM `+item.table).Scan(&maximum); err != nil {
			t.Fatal(err)
		}
		if err := target.QueryRowContext(ctx, `SELECT last_value, is_called FROM `+item.sequence).Scan(&lastValue, &isCalled); err != nil {
			t.Fatal(err)
		}
		if !isCalled || lastValue != maximum {
			t.Errorf("sequence %s: last=%d called=%t max=%d", item.sequence, lastValue, isCalled, maximum)
		}
	}
}
