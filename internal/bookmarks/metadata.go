package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
)

func (r *Repository) EnhanceMetadata(ctx context.Context, ownerID, id int64, title, description string) (Bookmark, error) {
	query := `UPDATE bookmarks_bookmark SET title = CASE WHEN title = '' THEN ` + r.marker(1) + ` ELSE title END,
		description = CASE WHEN description = '' THEN ` + r.marker(2) + ` ELSE description END
		WHERE owner_id = ` + r.marker(3) + ` AND id = ` + r.marker(4)
	result, err := r.db.ExecContext(ctx, query, title, description, ownerID, id)
	if err != nil {
		return Bookmark{}, fmt.Errorf("enhance bookmark metadata: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Bookmark{}, err
	}
	if rows != 1 {
		return Bookmark{}, sql.ErrNoRows
	}
	return r.GetByID(ctx, ownerID, id)
}
