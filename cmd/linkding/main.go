package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpserver"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/media"
	"github.com/juev/linkding/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "migrate-from-linkding" {
		return runMigration(ctx, os.Args[2:], os.Stdout)
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
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		return err
	}
	if err := ensureInitialSuperuser(ctx, db, cfg); err != nil {
		return err
	}
	if !cfg.DisableBackgroundTasks {
		if cfg.EnableSnapshots {
			count, err := media.RequeuePendingSnapshots(ctx, db, cfg.DBEngine)
			if err != nil {
				return err
			}
			if count > 0 {
				log.Printf("requeued %d pending snapshots", count)
			}
		}
		processor := media.New(db, cfg.DBEngine, cfg)
		worker := jobs.Worker{
			Queue:       jobs.New(db, cfg.DBEngine),
			Handlers:    processor.Handlers(),
			Lease:       3 * time.Minute,
			MaxAttempts: 6,
			OnError:     func(err error) { log.Printf("background job: %v", err) },
		}
		go func() {
			if err := worker.Run(ctx, 500*time.Millisecond); err != nil {
				log.Printf("background worker stopped: %v", err)
			}
		}()
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           httpserver.New(db, cfg, "web/static"),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}
