package main

import (
	"context"
	"fmt"
	"io"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/migratefrom"
	"github.com/juev/linkding/internal/store"
)

func runMigrateTasks(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: linkding migrate_tasks")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	count, exists, err := migratefrom.CountLegacyTasks(ctx, db, cfg.DBEngine)
	if err != nil {
		return err
	}
	if count != 0 {
		return migratefrom.LegacyTasksError(count)
	}
	if !exists {
		_, err = fmt.Fprintln(output, "Legacy task table does not exist. Skipping task migration")
	} else {
		_, err = fmt.Fprintln(output, "No legacy tasks found. Skipping task migration")
	}
	return err
}
