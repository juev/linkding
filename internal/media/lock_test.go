package media

import (
	"context"
	"errors"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestSnapshotLockSerializesProcessors(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	first := New(db, "sqlite", cfg)
	second := New(db, "sqlite", cfg)
	release, err := first.acquireSnapshotLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.acquireSnapshotLock(ctx); !errors.Is(err, ErrSnapshotBusy) {
		t.Fatalf("second processor: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release, err = second.acquireSnapshotLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
