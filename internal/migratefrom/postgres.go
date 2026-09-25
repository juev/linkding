package migratefrom

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/juev/linkding/internal/store"
)

type PostgresPreflight struct {
	SourceDir      string
	BookmarkCount  int64
	UserCount      int64
	AssetCount     int64
	LegacyTasks    int64
	PendingTasks   int64
	ScheduledTasks int64
}

type PostgresMigration struct {
	PostgresPreflight
	TargetDir   string
	CopiedFiles int
}

func InspectPostgres(ctx context.Context, sourceDSN, sourceDir string) (PostgresPreflight, error) {
	abs, err := filepath.Abs(sourceDir)
	if err != nil {
		return PostgresPreflight{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return PostgresPreflight{}, fmt.Errorf("source data directory is unavailable: %s", abs)
	}
	db, err := sql.Open("pgx", sourceDSN)
	if err != nil {
		return PostgresPreflight{}, fmt.Errorf("connect to source PostgreSQL: %w", err)
	}
	defer db.Close()
	var migration string
	if err := db.QueryRowContext(ctx, `SELECT name FROM django_migrations WHERE app = 'bookmarks' ORDER BY id DESC LIMIT 1`).Scan(&migration); err != nil || migration != upstreamBookmarkMigration {
		return PostgresPreflight{}, fmt.Errorf("source bookmarks schema must be linkding v1.47.0 (%s); update Python linkding first", upstreamBookmarkMigration)
	}
	report := PostgresPreflight{SourceDir: abs}
	for _, check := range []struct {
		query string
		count *int64
	}{
		{`SELECT count(*) FROM bookmarks_bookmark`, &report.BookmarkCount},
		{`SELECT count(*) FROM auth_user`, &report.UserCount},
		{`SELECT count(*) FROM bookmarks_bookmarkasset`, &report.AssetCount},
	} {
		if err := db.QueryRowContext(ctx, check.query).Scan(check.count); err != nil {
			return PostgresPreflight{}, fmt.Errorf("inspect source PostgreSQL: %w", err)
		}
	}
	report.LegacyTasks, _, err = CountLegacyTasks(ctx, db, "postgres")
	if err != nil {
		return PostgresPreflight{}, err
	}
	if err := verifyReferencedFiles(ctx, db, abs); err != nil {
		return PostgresPreflight{}, err
	}
	report.PendingTasks, report.ScheduledTasks, err = inspectHueyQueue(ctx, abs)
	if err != nil {
		return PostgresPreflight{}, err
	}
	return report, nil
}

// MigratePostgres copies a stopped upstream installation into an empty target
// PostgreSQL database. The source database and data directory remain untouched.
func MigratePostgres(ctx context.Context, sourceDSN, targetDSN, sourceDir, targetDir string) (PostgresMigration, error) {
	report, err := InspectPostgres(ctx, sourceDSN, sourceDir)
	if err != nil {
		return PostgresMigration{}, err
	}
	if report.LegacyTasks != 0 {
		return PostgresMigration{}, LegacyTasksError(report.LegacyTasks)
	}
	if report.PendingTasks != 0 || report.ScheduledTasks != 0 {
		return PostgresMigration{}, fmt.Errorf("Huey queue is not empty: %d tasks, %d scheduled; drain it before migration", report.PendingTasks, report.ScheduledTasks)
	}
	db, err := sql.Open("pgx", targetDSN)
	if err != nil {
		return PostgresMigration{}, err
	}
	defer db.Close()
	var existingTables int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema='public' AND table_type='BASE TABLE'`).Scan(&existingTables); err != nil {
		return PostgresMigration{}, fmt.Errorf("inspect target PostgreSQL: %w", err)
	}
	if existingTables != 0 {
		return PostgresMigration{}, fmt.Errorf("target PostgreSQL database must be empty; found %d tables", existingTables)
	}
	target, err := filepath.Abs(targetDir)
	if err != nil {
		return PostgresMigration{}, err
	}
	if target == report.SourceDir {
		return PostgresMigration{}, fmt.Errorf("target data directory must differ from source")
	}
	if relative, err := filepath.Rel(report.SourceDir, target); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return PostgresMigration{}, fmt.Errorf("target data directory cannot be inside source")
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return PostgresMigration{}, fmt.Errorf("create new target data directory: %w", err)
	}
	result := PostgresMigration{PostgresPreflight: report, TargetDir: target}
	result.CopiedFiles, err = copyDataFiles(report.SourceDir, target)
	if err != nil {
		return result, err
	}
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		return result, fmt.Errorf("prepare target PostgreSQL schema: %w", err)
	}
	sourceConn, err := pgx.Connect(ctx, sourceDSN)
	if err != nil {
		return result, err
	}
	defer sourceConn.Close(ctx)
	targetConn, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		return result, err
	}
	defer targetConn.Close(ctx)
	sourceTx, err := sourceConn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer sourceTx.Rollback(ctx)
	targetTx, err := targetConn.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer targetTx.Rollback(ctx)
	if _, err := targetTx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return result, err
	}
	sourceTables, err := postgresTables(ctx, sourceTx)
	if err != nil {
		return result, err
	}
	targetTables, err := postgresTables(ctx, targetTx)
	if err != nil {
		return result, err
	}
	targetUpstream := make([]string, 0, len(targetTables))
	for _, table := range targetTables {
		if table != "goose_db_version" && table != "linkding_job" && table != "linkding_lock" {
			targetUpstream = append(targetUpstream, table)
		}
	}
	if !slices.Equal(sourceTables, targetUpstream) {
		return result, fmt.Errorf("source and target upstream table sets differ: source=%v target=%v", sourceTables, targetUpstream)
	}
	for _, table := range sourceTables {
		identifier := (pgx.Identifier{"public", table}).Sanitize()
		var existing int64
		if err := targetTx.QueryRow(ctx, `SELECT count(*) FROM `+identifier).Scan(&existing); err != nil || existing != 0 {
			return result, fmt.Errorf("target table %s must be empty: count=%d err=%w", table, existing, err)
		}
		rows, err := sourceTx.Query(ctx, `SELECT * FROM `+identifier)
		if err != nil {
			return result, err
		}
		fields := rows.FieldDescriptions()
		columns := make([]string, len(fields))
		for i, field := range fields {
			columns[i] = field.Name
		}
		stream := &postgresRowSource{rows: rows}
		copied, copyErr := targetTx.CopyFrom(ctx, pgx.Identifier{"public", table}, columns, stream)
		rows.Close()
		if copyErr != nil {
			return result, fmt.Errorf("copy table %s: %w", table, copyErr)
		}
		if err := stream.Err(); err != nil {
			return result, fmt.Errorf("read table %s: %w", table, err)
		}
		var sourceCount, targetCount int64
		if err := sourceTx.QueryRow(ctx, `SELECT count(*) FROM `+identifier).Scan(&sourceCount); err != nil {
			return result, err
		}
		if err := targetTx.QueryRow(ctx, `SELECT count(*) FROM `+identifier).Scan(&targetCount); err != nil {
			return result, err
		}
		if copied != sourceCount || targetCount != sourceCount {
			return result, fmt.Errorf("table %s: copied=%d source=%d target=%d", table, copied, sourceCount, targetCount)
		}
	}
	for _, table := range sourceTables {
		if err := resetPostgresSequence(ctx, targetTx, table); err != nil {
			return result, err
		}
	}
	if err := sourceTx.Commit(ctx); err != nil {
		return result, err
	}
	if err := targetTx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func postgresTables(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

type postgresRowSource struct {
	rows   pgx.Rows
	values []any
	err    error
}

func (s *postgresRowSource) Next() bool {
	if !s.rows.Next() {
		return false
	}
	s.values, s.err = s.rows.Values()
	return s.err == nil
}
func (s *postgresRowSource) Values() ([]any, error) { return s.values, s.err }
func (s *postgresRowSource) Err() error {
	if s.err != nil {
		return s.err
	}
	return s.rows.Err()
}

func resetPostgresSequence(ctx context.Context, tx pgx.Tx, table string) error {
	identifier := (pgx.Identifier{"public", table}).Sanitize()
	var hasID bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema='public' AND table_name=$1 AND column_name='id')`, table).Scan(&hasID); err != nil {
		return err
	}
	if !hasID {
		return nil
	}
	var sequence sql.NullString
	if err := tx.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, 'id')`, identifier).Scan(&sequence); err != nil {
		return err
	}
	if !sequence.Valid {
		return nil
	}
	var maximum sql.NullInt64
	if err := tx.QueryRow(ctx, `SELECT max(id) FROM `+identifier).Scan(&maximum); err != nil {
		return err
	}
	value := int64(1)
	if maximum.Valid {
		value = maximum.Int64
	}
	var actual int64
	if err := tx.QueryRow(ctx, `SELECT setval($1::regclass, $2, $3)`, sequence.String, value, maximum.Valid).Scan(&actual); err != nil {
		return fmt.Errorf("reset sequence for %s: %w", table, err)
	}
	return nil
}
