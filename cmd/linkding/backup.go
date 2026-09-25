package main

import (
	"context"
	"fmt"
	"io"

	"github.com/juev/linkding/internal/backup"
	"github.com/juev/linkding/internal/config"
)

func runBackup(ctx context.Context, command string, args []string, output io.Writer) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("usage: linkding %s <destination>", command)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if command == "full_backup" {
		return backup.Full(ctx, cfg, args[0], output)
	}
	return backup.Database(ctx, cfg, args[0], output)
}
