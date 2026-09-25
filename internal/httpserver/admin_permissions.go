package httpserver

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/juev/linkding/internal/auth"
)

type adminPermissions struct {
	View, Add, Change, Delete bool
}

func (p adminPermissions) canList() bool { return p.View || p.Change }
func (p adminPermissions) any() bool     { return p.View || p.Add || p.Change || p.Delete }

func loadAdminPermissions(ctx context.Context, db *sql.DB, dialect string, user auth.User, app, model string) (adminPermissions, error) {
	if !user.IsActive || !user.IsStaff {
		return adminPermissions{}, nil
	}
	if user.IsSuperuser {
		return adminPermissions{View: true, Add: true, Change: true, Delete: true}, nil
	}
	query := `SELECT p.codename FROM auth_permission AS p
		JOIN django_content_type AS c ON c.id = p.content_type_id
		WHERE c.app_label = ` + assetMarker(dialect, 1) + ` AND c.model = ` + assetMarker(dialect, 2) + `
		AND (EXISTS (SELECT 1 FROM auth_user_user_permissions AS up WHERE up.permission_id = p.id AND up.user_id = ` + assetMarker(dialect, 3) + `)
		OR EXISTS (SELECT 1 FROM auth_user_groups AS ug JOIN auth_group_permissions AS gp ON gp.group_id = ug.group_id
			WHERE ug.user_id = ` + assetMarker(dialect, 4) + ` AND gp.permission_id = p.id))`
	rows, err := db.QueryContext(ctx, query, app, model, user.ID, user.ID)
	if err != nil {
		return adminPermissions{}, fmt.Errorf("load admin permissions: %w", err)
	}
	defer rows.Close()
	var permissions adminPermissions
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return adminPermissions{}, err
		}
		switch code {
		case "view_" + model:
			permissions.View = true
		case "add_" + model:
			permissions.Add = true
		case "change_" + model:
			permissions.Change = true
		case "delete_" + model:
			permissions.Delete = true
		}
	}
	return permissions, rows.Err()
}
