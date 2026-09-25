package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func writeAdminLog(ctx context.Context, tx *sql.Tx, engine string, actorID int64, app, model, objectID, repr string, actionFlag int, changeMessage string) error {
	var contentTypeID int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM django_content_type WHERE app_label = `+assetMarker(engine, 1)+` AND model = `+assetMarker(engine, 2),
		app, model,
	).Scan(&contentTypeID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("admin log content type %s.%s not found", app, model)
	}
	if err != nil {
		return fmt.Errorf("look up admin log content type %s.%s: %w", app, model, err)
	}

	if chars := []rune(repr); len(chars) > 200 {
		repr = string(chars[:200])
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO django_admin_log(action_time,object_id,object_repr,action_flag,change_message,content_type_id,user_id) VALUES (`+
			assetMarker(engine, 1)+`,`+assetMarker(engine, 2)+`,`+assetMarker(engine, 3)+`,`+assetMarker(engine, 4)+`,`+assetMarker(engine, 5)+`,`+assetMarker(engine, 6)+`,`+assetMarker(engine, 7)+`)`,
		time.Now().UTC(), objectID, repr, actionFlag, changeMessage, contentTypeID, actorID,
	)
	if err != nil {
		return fmt.Errorf("write admin log: %w", err)
	}
	return nil
}
