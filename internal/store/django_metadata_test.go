package store

import (
	"context"
	"testing"
)

func TestMigrateSeedsPinnedDjangoMetadataIdempotently(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, db, "sqlite"); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
	}
	var contentTypes, permissions int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM django_content_type`).Scan(&contentTypes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_permission`).Scan(&permissions); err != nil {
		t.Fatal(err)
	}
	if contentTypes != 17 || permissions != 68 {
		t.Fatalf("metadata counts = %d content types, %d permissions; want 17 and 68", contentTypes, permissions)
	}
	var gotName string
	if err := db.QueryRowContext(ctx, `SELECT p.name FROM auth_permission p JOIN django_content_type c ON c.id=p.content_type_id WHERE c.app_label='authtoken' AND c.model='tokenproxy' AND p.codename='view_tokenproxy'`).Scan(&gotName); err != nil {
		t.Fatal(err)
	}
	if gotName != "Can view Token" {
		t.Fatalf("TokenProxy view permission name = %q, want %q", gotName, "Can view Token")
	}
}

func TestMigrateImportedPreservesExistingDjangoMetadata(t *testing.T) {
	ctx := context.Background()
	db := openTestSQLite(t)
	if err := Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO django_content_type(id, app_label, model) VALUES (900, 'custom', 'record')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_permission(id, content_type_id, codename, name) VALUES (901, 900, 'view_record', 'Imported custom permission')`); err != nil {
		t.Fatal(err)
	}
	if err := MigrateImported(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	var contentTypeID, permissionID int
	var permissionName string
	if err := db.QueryRowContext(ctx, `SELECT c.id, p.id, p.name FROM django_content_type c JOIN auth_permission p ON p.content_type_id=c.id WHERE c.app_label='custom' AND c.model='record' AND p.codename='view_record'`).Scan(&contentTypeID, &permissionID, &permissionName); err != nil {
		t.Fatal(err)
	}
	if contentTypeID != 900 || permissionID != 901 || permissionName != "Imported custom permission" {
		t.Fatalf("existing metadata changed: content type id=%d permission id=%d name=%q", contentTypeID, permissionID, permissionName)
	}
	var contentTypes, permissions int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM django_content_type`).Scan(&contentTypes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_permission`).Scan(&permissions); err != nil {
		t.Fatal(err)
	}
	if contentTypes != 18 || permissions != 69 {
		t.Fatalf("metadata counts = %d content types, %d permissions; want 18 and 69", contentTypes, permissions)
	}
}
