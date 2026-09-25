package httpserver

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestWriteAdminLog(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_user(id,username,password,first_name,last_name,email,is_staff,is_active,is_superuser,date_joined) VALUES (1,'admin','!','','','',1,1,1,?)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	repr := strings.Repeat("é", 201)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAdminLog(ctx, tx, cfg.DBEngine, 1, "bookmarks", "bookmark", "42", repr, 2, "changed title"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var contentTypeID, actionFlag int
	var objectID, gotRepr, message string
	var actionTime time.Time
	if err := db.QueryRowContext(ctx, `SELECT content_type_id,object_id,object_repr,action_flag,change_message,action_time FROM django_admin_log`).Scan(&contentTypeID, &objectID, &gotRepr, &actionFlag, &message, &actionTime); err != nil {
		t.Fatal(err)
	}
	if contentTypeID != int(testContentTypeID(t, db, "bookmarks", "bookmark")) || objectID != "42" || gotRepr != strings.Repeat("é", 200) || actionFlag != 2 || message != "changed title" {
		t.Fatalf("unexpected log row: content_type=%d object_id=%q repr-runes=%d flag=%d message=%q", contentTypeID, objectID, len([]rune(gotRepr)), actionFlag, message)
	}
	if delta := time.Since(actionTime); delta < 0 || delta > time.Minute {
		t.Fatalf("action_time is not a recent UTC instant: %s", actionTime)
	}

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAdminLog(ctx, tx, cfg.DBEngine, 999, "bookmarks", "bookmark", "43", "invalid actor", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("expected invalid actor foreign key error")
	} else if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM django_admin_log`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("invalid actor left a log row: count=%d", count)
	}
}
