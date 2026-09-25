package store

import (
	"context"
	"database/sql"
	"fmt"
)

type djangoContentType struct {
	appLabel string
	model    string
	name     string
}

var pinnedDjangoContentTypes = []djangoContentType{
	{"bookmarks", "apitoken", "api token"},
	{"bookmarks", "bookmark", "bookmark"},
	{"bookmarks", "bookmarkasset", "bookmark asset"},
	{"bookmarks", "bookmarkbundle", "bookmark bundle"},
	{"bookmarks", "feedtoken", "feed token"},
	{"bookmarks", "globalsettings", "global settings"},
	{"bookmarks", "tag", "tag"},
	{"bookmarks", "toast", "toast"},
	{"bookmarks", "userprofile", "user profile"},
	{"admin", "logentry", "log entry"},
	{"auth", "group", "group"},
	{"auth", "permission", "permission"},
	{"auth", "user", "user"},
	{"contenttypes", "contenttype", "content type"},
	{"sessions", "session", "session"},
	{"authtoken", "token", "Token"},
	{"authtoken", "tokenproxy", "Token"},
}

var djangoPermissionVerbs = []struct {
	verb     string
	codename string
}{
	{"add", "add"},
	{"change", "change"},
	{"delete", "delete"},
	{"view", "view"},
}

// seedDjangoMetadata creates the content types and default model permissions
// Django installs in a fresh v1.47.0 database. Existing rows are left intact.
func seedDjangoMetadata(ctx context.Context, db *sql.DB, engine string) error {
	if engine != "sqlite" && engine != "postgres" {
		return fmt.Errorf("unsupported database engine %q", engine)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata transaction: %w", err)
	}
	defer tx.Rollback()

	placeholder := "?"
	if engine == "postgres" {
		placeholder = "$1"
	}
	insertContentType := `INSERT INTO django_content_type (app_label, model) VALUES (` + placeholder + `, `
	if engine == "postgres" {
		insertContentType += "$2"
	} else {
		insertContentType += "?"
	}
	insertContentType += `) ON CONFLICT (app_label, model) DO NOTHING`
	selectContentType := `SELECT id FROM django_content_type WHERE app_label = ` + placeholder
	if engine == "postgres" {
		selectContentType += " AND model = $2"
	} else {
		selectContentType += " AND model = ?"
	}
	insertPermission := `INSERT INTO auth_permission (content_type_id, codename, name) VALUES (` + placeholder + `, `
	if engine == "postgres" {
		insertPermission += "$2, $3"
	} else {
		insertPermission += "?, ?"
	}
	insertPermission += `) ON CONFLICT (content_type_id, codename) DO NOTHING`

	for _, contentType := range pinnedDjangoContentTypes {
		if _, err := tx.ExecContext(ctx, insertContentType, contentType.appLabel, contentType.model); err != nil {
			return fmt.Errorf("insert content type %s.%s: %w", contentType.appLabel, contentType.model, err)
		}
		var contentTypeID int64
		if err := tx.QueryRowContext(ctx, selectContentType, contentType.appLabel, contentType.model).Scan(&contentTypeID); err != nil {
			return fmt.Errorf("find content type %s.%s: %w", contentType.appLabel, contentType.model, err)
		}
		for _, permission := range djangoPermissionVerbs {
			codename := permission.codename + "_" + contentType.model
			name := "Can " + permission.verb + " " + contentType.name
			if _, err := tx.ExecContext(ctx, insertPermission, contentTypeID, codename, name); err != nil {
				return fmt.Errorf("insert permission %s: %w", codename, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata transaction: %w", err)
	}
	return nil
}
