package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/pressly/goose/v3"
)

func openTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), config.Config{
		DBEngine: "sqlite",
		DataDir:  filepath.Join(t.TempDir(), "nested-data"),
	})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLiteOpenCreatesFileAndEnablesForeignKeys(t *testing.T) {
	db := openTestSQLite(t)
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", enabled)
	}
}

func TestSQLiteMigrationCreatesPinnedSchemaAndIsRepeatable(t *testing.T) {
	db := openTestSQLite(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(context.Background(), db, "sqlite"); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
	}
	assertSQLiteURLIndexes(t, db, true)
	var tables int
	err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'").Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 23 {
		t.Fatalf("got %d tables, want 21 upstream plus Go job queue and snapshot lock", tables)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check: %s", integrity)
	}
}

func TestMigrateImportedSkipsExistingUpstreamSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)
	if err := Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	// The source fixture must have the pinned Python index, not the Go upgrade.
	for _, statement := range []string{
		`DROP INDEX bm_norm_owner_idx`,
		`CREATE INDEX bookmarks_bookmark_url_normalized_8b3c53e4 ON bookmarks_bookmark(url_normalized)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"linkding_lock", "linkding_job", "goose_db_version"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateImported(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatalf("normal migrations after import: %v", err)
	}
	for _, table := range []string{"linkding_lock", "linkding_job"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("imported table %s: count=%d err=%v", table, count, err)
		}
	}
	assertSQLiteURLIndexes(t, db, true)
}

func TestSQLiteURLIndexMigrationPreservesPopulatedBookmarkTable(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)
	folder, err := fs.Sub(migrationFiles, "migrations/sqlite")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, folder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	assertSQLiteURLIndexes(t, db, false)
	now := time.Now().UTC()
	result, err := db.ExecContext(ctx, `INSERT INTO auth_user
		(password,last_login,is_superuser,username,first_name,last_name,email,is_staff,is_active,date_joined)
		VALUES ('',NULL,0,'migration','','','',0,1,?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		url := fmt.Sprintf("https://example.test/%d", i)
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark
			(url,url_normalized,title,description,notes,website_title,website_description,unread,is_archived,
			 shared,date_added,date_modified,owner_id,web_archive_snapshot_url,favicon_file,preview_image_file)
			VALUES (?,?,?,?,?,NULL,NULL,0,0,0,?,?,?,'','','')`,
			url, url, "", "", "", now, now, ownerID); err != nil {
			t.Fatal(err)
		}
	}
	var originalRoot int
	if err := db.QueryRowContext(ctx, `SELECT rootpage FROM sqlite_master WHERE type='table' AND name='bookmarks_bookmark'`).Scan(&originalRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assertSQLiteURLIndexes(t, db, true)
	assertSQLiteBookmarkTableUnchanged(t, db, originalRoot, 100)
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	assertSQLiteURLIndexes(t, db, false)
	assertSQLiteBookmarkTableUnchanged(t, db, originalRoot, 100)
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assertSQLiteURLIndexes(t, db, true)
	assertSQLiteBookmarkTableUnchanged(t, db, originalRoot, 100)
}

func assertSQLiteURLIndexes(t *testing.T, db *sql.DB, upgraded bool) {
	t.Helper()
	for _, item := range []struct {
		name    string
		columns []string
		want    bool
	}{
		{"bookmarks_bookmark_url_normalized_8b3c53e4", []string{"url_normalized"}, !upgraded},
		{"bm_norm_owner_idx", []string{"url_normalized", "owner_id"}, upgraded},
	} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, item.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != item.want {
			t.Fatalf("index %s count=%d, want present=%t", item.name, count, item.want)
		}
		if !item.want {
			continue
		}
		rows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, item.name)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, item.columns) {
			t.Fatalf("index %s columns=%v, want %v", item.name, got, item.columns)
		}
	}
}

func assertSQLiteBookmarkTableUnchanged(t *testing.T, db *sql.DB, originalRoot, originalCount int) {
	t.Helper()
	var root, count int
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type='table' AND name='bookmarks_bookmark'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM bookmarks_bookmark`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if root != originalRoot || count != originalCount {
		t.Fatalf("bookmark table changed: root=%d/%d count=%d/%d", root, originalRoot, count, originalCount)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check=%q err=%v", integrity, err)
	}
}

func TestPostgresMigrationCreatesPinnedSchemaAndIsRepeatable(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ {
		if err := Migrate(context.Background(), db, "postgres"); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
	}
	var tables int
	err = db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' AND table_name <> 'goose_db_version'").Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 23 {
		t.Fatalf("got %d tables, want 21 upstream plus Go job queue and snapshot lock", tables)
	}
}

func TestSQLiteUnicodeFunctions(t *testing.T) {
	db := openTestSQLite(t)
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"Cyrillic equality", "SELECT ld_ci_equal('МОСКВА', 'москва')", 1},
		{"German substring", "SELECT ld_ci_contains('Grüße', 'GRÜ')", 1},
		{"German sharp s stays distinct from ss in ICU LIKE", "SELECT ld_ci_equal('straße', 'STRASSE')", 0},
		{"German sharp s stays distinct in pattern matching", "SELECT ld_like('STRASSE', 'straße', '')", 0},
		{"dotless i stays distinct in ICU LIKE", "SELECT ld_ci_equal('I', 'ı')", 0},
		{"Greek final sigma matches", "SELECT ld_ci_equal('ΟΣ', 'ος')", 1},
		{"percent wildcard", "SELECT ld_like('%ис%', 'Письмо', '')", 1},
		{"single rune wildcard", "SELECT ld_like('_bc', 'Abc', '')", 1},
		{"escaped wildcard", "SELECT ld_like('100\\%', '100%', '\\')", 1},
		{"escaped wildcard mismatch", "SELECT ld_like('100\\%', '1000', '\\')", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			if err := db.QueryRow(tc.query).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
	var got sql.NullInt64
	if err := db.QueryRow("SELECT ld_ci_contains(NULL, 'a')").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got.Valid {
		t.Fatalf("NULL input gave %d", got.Int64)
	}
	for _, tc := range []struct {
		name, query string
		want        sql.NullInt64
	}{
		{"first field", "SELECT ld_ci_contains_any('SYSTEMS', 'other', 'other', 'other', 'systems')", sql.NullInt64{Int64: 1, Valid: true}},
		{"last field", "SELECT ld_ci_contains_any('other', 'other', 'other', 'Systems URL', 'systems')", sql.NullInt64{Int64: 1, Valid: true}},
		{"Unicode fold", "SELECT ld_ci_contains_any('other', 'kelvin', 'other', 'other', 'K')", sql.NullInt64{Int64: 1, Valid: true}},
		{"no match", "SELECT ld_ci_contains_any('a', 'b', 'c', 'd', 'z')", sql.NullInt64{Int64: 0, Valid: true}},
		{"unknown", "SELECT ld_ci_contains_any('a', NULL, 'c', 'd', 'z')", sql.NullInt64{}},
		{"match beats unknown", "SELECT ld_ci_contains_any('z', NULL, 'c', 'd', 'z')", sql.NullInt64{Int64: 1, Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actual sql.NullInt64
			if err := db.QueryRow(tc.query).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if actual != tc.want {
				t.Fatalf("got %+v, want %+v", actual, tc.want)
			}
		})
	}
}

func TestASCIIContainsFoldMatchesUnicodeFallback(t *testing.T) {
	for left := byte(0); left < 128; left++ {
		for right := byte(0); right < 128; right++ {
			text, needle := string([]byte{left}), string([]byte{right})
			want := strings.Contains(simpleCaseFold(text), simpleCaseFold(needle))
			if got := asciiContainsFold(text, needle); got != want {
				t.Fatalf("ASCII fold mismatch for %q and %q: got %t, want %t", text, needle, got, want)
			}
		}
	}
	for _, tc := range []struct{ text, needle string }{
		{"Systems article", "systems"},
		{"SYSTEMS article", "TeMs"},
		{"systematic", "systems"},
		{"A short personal note", ""},
		{"path/100%_value", "%_VAL"},
	} {
		want := strings.Contains(simpleCaseFold(tc.text), simpleCaseFold(tc.needle))
		if got := asciiContainsFold(tc.text, tc.needle); got != want {
			t.Fatalf("ASCII fold mismatch for %q and %q: got %t, want %t", tc.text, tc.needle, got, want)
		}
	}
	db := openTestSQLite(t)
	var found int
	if err := db.QueryRow(`SELECT ld_ci_contains('kelvin', 'K')`).Scan(&found); err != nil || found != 1 {
		t.Fatalf("Unicode fallback changed: found=%d err=%v", found, err)
	}
}

func TestASCIIEqualFoldMatchesUnicodeFallback(t *testing.T) {
	for left := byte(0); left < 128; left++ {
		for right := byte(0); right < 128; right++ {
			first, second := string([]byte{left}), string([]byte{right})
			want := simpleCaseFold(first) == simpleCaseFold(second)
			if got := strings.EqualFold(first, second); got != want {
				t.Fatalf("ASCII equality mismatch for %q and %q: got %t, want %t", first, second, got, want)
			}
		}
	}
	for _, tc := range []struct{ left, right string }{
		{"tag-008", "TAG-008"},
		{"tag-008", "tag-009"},
		{"Systems", "systems"},
	} {
		want := simpleCaseFold(tc.left) == simpleCaseFold(tc.right)
		if got := strings.EqualFold(tc.left, tc.right); got != want {
			t.Fatalf("ASCII equality mismatch for %q and %q: got %t, want %t", tc.left, tc.right, got, want)
		}
	}
	db := openTestSQLite(t)
	var found int
	if err := db.QueryRow(`SELECT ld_ci_equal('kelvin', 'KELVIN')`).Scan(&found); err != nil || found != 1 {
		t.Fatalf("Unicode equality fallback changed: found=%d err=%v", found, err)
	}
}

func TestSQLiteUnicodeCollationIsAvailableOnNewConnection(t *testing.T) {
	db := openTestSQLite(t)
	db.SetMaxOpenConns(2)
	var count int
	err := db.QueryRow("SELECT count(*) FROM (SELECT 'z' AS title UNION ALL SELECT 'a') ORDER BY title COLLATE LD_ROOT").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d", count)
	}
}

func TestSQLiteUnicodeOrderingMatchesPinnedICUSample(t *testing.T) {
	db := openTestSQLite(t)
	if _, err := db.Exec("CREATE TABLE sample (title TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	input := []string{"Zoo", "Äpfel", "apple", "äpfel", "İzmir", "Istanbul", "ı", "ß", "ss", "ΟΣ", "ος", "моСКва", "Москва", "Café", "CAFÉ"}
	for _, title := range input {
		if _, err := db.Exec("INSERT INTO sample (title) VALUES (?)", title); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT title FROM sample ORDER BY ld_lower(title) COLLATE LD_ROOT")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		got = append(got, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Captured from the pinned v1.47.0 image with libicu.so loaded.
	want := []string{"Äpfel", "äpfel", "apple", "Café", "CAFÉ", "Istanbul", "İzmir", "ı", "ss", "ß", "Zoo", "ΟΣ", "ος", "моСКва", "Москва"}
	if !slices.Equal(got, want) {
		t.Fatalf("ICU order mismatch\n got: %q\nwant: %q", got, want)
	}
}
