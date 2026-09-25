package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/juev/linkding/internal/jobs"
)

// RequeuePendingSnapshots restores assets left pending by a stopped consumer or
// by a period when snapshot processing was disabled. Duplicate jobs are safe:
// ProcessSnapshot checks the asset status again after taking the global lock.
func RequeuePendingSnapshots(ctx context.Context, db *sql.DB, engine string) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM bookmarks_bookmarkasset
		WHERE asset_type = 'snapshot' AND status = 'pending' ORDER BY date_created, id`)
	if err != nil {
		return 0, fmt.Errorf("find pending snapshots: %w", err)
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
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	queue := jobs.New(db, engine)
	for _, id := range ids {
		payload, _ := json.Marshal(struct {
			AssetID int64 `json:"asset_id"`
		}{id})
		if _, err := queue.EnqueueTx(ctx, tx, "process_snapshot", payload); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}
