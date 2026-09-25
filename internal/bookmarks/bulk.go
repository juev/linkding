package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SetState updates only bookmarks owned by the caller, as upstream bulk
// actions do. Unknown or foreign IDs are ignored.
func (r *Repository) SetState(ctx context.Context, ownerID int64, ids []int64, field string, value bool) error {
	if field != "is_archived" && field != "unread" && field != "shared" {
		return fmt.Errorf("unsupported bookmark state field %q", field)
	}
	if len(ids) == 0 {
		return nil
	}
	args := []any{value, time.Now().UTC(), ownerID}
	markers := make([]string, len(ids))
	for index, id := range ids {
		args = append(args, id)
		markers[index] = r.marker(index + 4)
	}
	query := `UPDATE bookmarks_bookmark SET ` + field + ` = ` + r.marker(1) + `, date_modified = ` + r.marker(2) + ` WHERE owner_id = ` + r.marker(3) + ` AND id IN (` + strings.Join(markers, ",") + `)`
	_, err := r.db.ExecContext(ctx, query, args...)
	return err
}

func (r *Repository) TagMany(ctx context.Context, ownerID int64, ids []int64, rawTags string, add bool) error {
	names := ParseTagString(strings.ReplaceAll(rawTags, " ", ","), ",")
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tagIDs := make([]int64, 0, len(names))
	for _, name := range names {
		query := `SELECT id FROM bookmarks_tag WHERE owner_id = ` + r.marker(1) + ` AND `
		if r.engine == "postgres" {
			query += `UPPER(name)=UPPER(` + r.marker(2) + `)`
		} else {
			query += `ld_ci_equal(name, ` + r.marker(2) + `) = 1`
		}
		query += ` ORDER BY id LIMIT 1`
		var id int64
		err := tx.QueryRowContext(ctx, query, ownerID, name).Scan(&id)
		if err == sql.ErrNoRows {
			query = `INSERT INTO bookmarks_tag (name,date_added,owner_id) VALUES (` + placeholders(r.engine, 3) + `) RETURNING id`
			err = tx.QueryRowContext(ctx, query, name, time.Now().UTC(), ownerID).Scan(&id)
		}
		if err != nil {
			return err
		}
		tagIDs = append(tagIDs, id)
	}
	for _, id := range ids {
		var exists bool
		query := `SELECT EXISTS(SELECT 1 FROM bookmarks_bookmark WHERE id = ` + r.marker(1) + ` AND owner_id = ` + r.marker(2) + `)`
		if err := tx.QueryRowContext(ctx, query, id, ownerID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		for _, tagID := range tagIDs {
			if add {
				query = `INSERT INTO bookmarks_bookmark_tags (bookmark_id,tag_id) VALUES (` + placeholders(r.engine, 2) + `) ON CONFLICT DO NOTHING`
			} else {
				query = `DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id = ` + r.marker(1) + ` AND tag_id = ` + r.marker(2)
			}
			if _, err := tx.ExecContext(ctx, query, id, tagID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bookmarks_bookmark SET date_modified = `+r.marker(1)+` WHERE id = `+r.marker(2), time.Now().UTC(), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
