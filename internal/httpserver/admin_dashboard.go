package httpserver

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/config"
)

type adminDashboardApp struct {
	Slug, Label, Path string
	Models            []adminModelLink
}

type adminRecentAction struct {
	Class, Verb, Object, Link, ContentType string
}

func groupAdminDashboardApps(cfg config.Config, models []adminModelLink) []adminDashboardApp {
	var apps []adminDashboardApp
	for _, model := range models {
		if len(apps) == 0 || apps[len(apps)-1].Slug != model.App {
			apps = append(apps, adminDashboardApp{Slug: model.App, Label: model.AppLabel, Path: cfg.URLPrefix() + "admin/" + model.App + "/"})
		}
		apps[len(apps)-1].Models = append(apps[len(apps)-1].Models, model)
	}
	return apps
}

func loadAdminRecentActions(ctx context.Context, db *sql.DB, cfg config.Config, userID int64) ([]adminRecentAction, error) {
	query := `SELECT l.action_flag,l.object_id,l.object_repr,c.app_label,c.model
		FROM django_admin_log AS l LEFT JOIN django_content_type AS c ON c.id=l.content_type_id
		WHERE l.user_id=` + assetMarker(cfg.DBEngine, 1) + ` ORDER BY l.action_time DESC,l.id DESC LIMIT 10`
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var actions []adminRecentAction
	for rows.Next() {
		var flag int
		var objectID, app, model sql.NullString
		var object string
		if err := rows.Scan(&flag, &objectID, &object, &app, &model); err != nil {
			return nil, err
		}
		action := adminRecentAction{Object: object, ContentType: "Unknown content"}
		switch flag {
		case 1:
			action.Class, action.Verb = "addlink", "Added:"
		case 2:
			action.Class, action.Verb = "changelink", "Changed:"
		case 3:
			action.Class, action.Verb = "deletelink", "Deleted:"
		}
		if app.Valid && model.Valid {
			if definition, found := findAdminModel(app.String, model.String); found {
				action.ContentType = definition.Label
				if definition.Model == "apitoken" {
					action.ContentType = "Api token"
				}
				if flag != 3 && objectID.Valid && objectID.String != "" {
					action.Link = cfg.URLPrefix() + "admin/" + app.String + "/" + model.String + "/" + url.PathEscape(objectID.String) + "/change/"
				}
			} else if model.String != "" {
				action.ContentType = strings.ToUpper(model.String[:1]) + model.String[1:]
			}
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}
