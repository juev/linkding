package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func EnqueueRefreshFavicons(ctx context.Context, db *sql.DB, engine string, ownerID int64) error {
	var enabled bool
	if err := db.QueryRowContext(ctx, "SELECT enable_favicons FROM bookmarks_userprofile WHERE user_id = "+settingsMarker(engine, 1), ownerID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	rows, err := db.QueryContext(ctx, "SELECT id FROM bookmarks_bookmark WHERE owner_id = "+settingsMarker(engine, 1)+" ORDER BY id", ownerID)
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
	queue := New(db, engine)
	for _, id := range ids {
		payload, _ := json.Marshal(struct {
			BookmarkID int64 `json:"bookmark_id"`
		}{id})
		if _, err := queue.Enqueue(ctx, "load_favicon", payload); err != nil {
			return err
		}
	}
	return nil
}

func EnqueueMissingSnapshots(ctx context.Context, db *sql.DB, engine string, ownerID int64) (int, error) {
	query := `SELECT b.id FROM bookmarks_bookmark b WHERE b.owner_id = ` + settingsMarker(engine, 1) + `
		AND NOT EXISTS (SELECT 1 FROM bookmarks_bookmarkasset a WHERE a.bookmark_id = b.id
		AND a.asset_type = 'snapshot' AND a.status IN ('pending', 'complete')) ORDER BY b.id`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := EnqueueSnapshot(ctx, db, engine, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func EnqueueSnapshot(ctx context.Context, db *sql.DB, engine string, bookmarkID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert := `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (`
	for index := 1; index <= 9; index++ {
		if index > 1 {
			insert += ","
		}
		insert += settingsMarker(engine, index)
	}
	insert += `) RETURNING id`
	var assetID int64
	if err := tx.QueryRowContext(ctx, insert, time.Now().UTC(), "", nil, "snapshot", "", "New snapshot", "pending", false, bookmarkID).Scan(&assetID); err != nil {
		return err
	}
	payload, _ := json.Marshal(struct {
		AssetID int64 `json:"asset_id"`
	}{assetID})
	if _, err := New(db, engine).EnqueueTx(ctx, tx, "process_snapshot", payload); err != nil {
		return err
	}
	return tx.Commit()
}

func settingsMarker(engine string, index int) string {
	if engine == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}
