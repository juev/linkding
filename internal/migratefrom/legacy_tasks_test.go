package migratefrom

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func TestCountLegacyTasksPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if count, exists, err := CountLegacyTasks(ctx, db, "postgres"); err != nil || exists || count != 0 {
		t.Fatalf("missing table: count=%d exists=%v err=%v", count, exists, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE background_task (id bigint primary key)`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := db.ExecContext(context.Background(), `DROP TABLE background_task`); err != nil {
			t.Errorf("drop legacy table: %v", err)
		}
	}()
	if count, exists, err := CountLegacyTasks(ctx, db, "postgres"); err != nil || !exists || count != 0 {
		t.Fatalf("empty table: count=%d exists=%v err=%v", count, exists, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO background_task (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if count, exists, err := CountLegacyTasks(ctx, db, "postgres"); err != nil || !exists || count != 1 {
		t.Fatalf("queued table: count=%d exists=%v err=%v", count, exists, err)
	}
}
