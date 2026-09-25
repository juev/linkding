package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// EnqueueMissingMedia schedules existing bookmarks that lack an enabled media
// type. The profile settings handler calls it only for newly enabled types;
// the Netscape importer calls it for every currently enabled type.
func EnqueueMissingMedia(ctx context.Context, db *sql.DB, engine string, ownerID int64, favicons, previews bool) error {
	queue := New(db, engine)
	for _, kind := range []struct {
		enabled    bool
		field, job string
	}{{favicons, "favicon_file", "load_favicon"}, {previews, "preview_image_file", "load_preview_image"}} {
		if !kind.enabled {
			continue
		}
		marker := "?"
		if engine == "postgres" {
			marker = "$1"
		}
		query := "SELECT id FROM bookmarks_bookmark WHERE owner_id = " + marker + " AND " + kind.field + " = '' ORDER BY id"
		rows, err := db.QueryContext(ctx, query, ownerID)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			payload, err := json.Marshal(struct {
				BookmarkID int64 `json:"bookmark_id"`
			}{id})
			if err != nil {
				return fmt.Errorf("encode media job for bookmark %d: %w", id, err)
			}
			if _, err := queue.Enqueue(ctx, kind.job, payload); err != nil {
				return err
			}
		}
	}
	return nil
}
