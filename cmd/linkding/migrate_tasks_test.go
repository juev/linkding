package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestMigrateTasksReportsEmptyAndRejectsLegacyQueue(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LD_DB_ENGINE", "sqlite")
	ctx := context.Background()
	var output bytes.Buffer
	if err := runMigrateTasks(ctx, nil, &output); err != nil || !strings.Contains(output.String(), "table does not exist") {
		t.Fatalf("missing legacy table: output=%q err=%v", output.String(), err)
	}
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: "data"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE background_task (id integer primary key, task_name text, task_params text)`); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runMigrateTasks(ctx, nil, &output); err != nil || !strings.Contains(output.String(), "No legacy tasks found") {
		t.Fatalf("empty legacy table: output=%q err=%v", output.String(), err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO background_task (id, task_name, task_params) VALUES (1, 'legacy.task', '[[]]')`); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runMigrateTasks(ctx, nil, &output); err == nil || !strings.Contains(err.Error(), "1 legacy background tasks remain") || output.Len() != 0 {
		t.Fatalf("queued legacy task: output=%q err=%v", output.String(), err)
	}
	args := os.Args
	os.Args = []string{"linkding"}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err == nil || !strings.Contains(err.Error(), "1 legacy background tasks remain") {
		t.Fatalf("server accepted legacy task: %v", err)
	}
}
