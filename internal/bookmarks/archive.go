package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (r *Repository) SetArchived(ctx context.Context, ownerID, id int64, archived bool) error {
	query := `UPDATE bookmarks_bookmark SET is_archived = ` + r.marker(1) + `, date_modified = ` + r.marker(2) +
		` WHERE owner_id = ` + r.marker(3) + ` AND id = ` + r.marker(4)
	result, err := r.db.ExecContext(ctx, query, archived, time.Now().UTC(), ownerID, id)
	if err != nil {
		return fmt.Errorf("set bookmark archive state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}
