package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
)

type DeletedFiles struct {
	Preview string
	Assets  []string
}

// DeleteData mirrors Django's bookmark cascade and returns files for cleanup
// after the transaction commits. Favicon files remain shared by other links.
func (r *Repository) DeleteData(ctx context.Context, ownerID, id int64) (DeletedFiles, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return DeletedFiles{}, err
	}
	defer tx.Rollback()
	var files DeletedFiles
	query := `SELECT preview_image_file FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) + ` AND id = ` + r.marker(2)
	if r.engine == "postgres" {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, query, ownerID, id).Scan(&files.Preview); err != nil {
		return DeletedFiles{}, err
	}
	query = `SELECT file FROM bookmarks_bookmarkasset WHERE bookmark_id = ` + r.marker(1)
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return DeletedFiles{}, err
	}
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			rows.Close()
			return DeletedFiles{}, err
		}
		if file != "" {
			files.Assets = append(files.Assets, file)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return DeletedFiles{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id = `+r.marker(1), id); err != nil {
		return DeletedFiles{}, fmt.Errorf("delete bookmark tags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bookmarks_bookmark SET latest_snapshot_id = NULL WHERE id = `+r.marker(1), id); err != nil {
		return DeletedFiles{}, fmt.Errorf("clear latest snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmarks_bookmarkasset WHERE bookmark_id = `+r.marker(1), id); err != nil {
		return DeletedFiles{}, fmt.Errorf("delete bookmark assets: %w", err)
	}
	query = `DELETE FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) + ` AND id = ` + r.marker(2)
	result, err := tx.ExecContext(ctx, query, ownerID, id)
	if err != nil {
		return DeletedFiles{}, fmt.Errorf("delete bookmark: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return DeletedFiles{}, err
	}
	if count != 1 {
		return DeletedFiles{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return DeletedFiles{}, err
	}
	return files, nil
}
