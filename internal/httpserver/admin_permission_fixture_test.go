package httpserver

import (
	"context"
	"database/sql"
	"testing"
)

func testPermissionID(t *testing.T, db *sql.DB, app, model, codename string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(context.Background(), `SELECT p.id FROM auth_permission p JOIN django_content_type c ON c.id=p.content_type_id WHERE c.app_label=? AND c.model=? AND p.codename=?`, app, model, codename).Scan(&id)
	if err != nil {
		t.Fatalf("lookup permission %s.%s.%s: %v", app, model, codename, err)
	}
	return id
}

func testContentTypeID(t *testing.T, db *sql.DB, app, model string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM django_content_type WHERE app_label=? AND model=?`, app, model).Scan(&id); err != nil {
		t.Fatalf("lookup content type %s.%s: %v", app, model, err)
	}
	return id
}

func grantTestUserPermission(t *testing.T, db *sql.DB, userID int64, app, model, codename string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO auth_user_user_permissions(user_id,permission_id) VALUES (?,?)`, userID, testPermissionID(t, db, app, model, codename)); err != nil {
		t.Fatalf("grant user permission %s.%s.%s: %v", app, model, codename, err)
	}
}

func grantTestGroupPermission(t *testing.T, db *sql.DB, groupID int64, app, model, codename string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO auth_group_permissions(group_id,permission_id) VALUES (?,?)`, groupID, testPermissionID(t, db, app, model, codename)); err != nil {
		t.Fatalf("grant group permission %s.%s.%s: %v", app, model, codename, err)
	}
}
