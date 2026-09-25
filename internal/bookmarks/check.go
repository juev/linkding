package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (r *Repository) FindExisting(ctx context.Context, ownerID int64, url string) (Bookmark, error) {
	query := `SELECT id FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) +
		` AND (url_normalized = ` + r.marker(2) + ` OR (url_normalized = '' AND url = ` + r.marker(3) + `)) ORDER BY id LIMIT 1`
	var id int64
	if err := r.db.QueryRowContext(ctx, query, ownerID, NormalizeURL(url), url).Scan(&id); err != nil {
		return Bookmark{}, err
	}
	return r.GetByID(ctx, ownerID, id)
}

func (r *Repository) AutoTagsForURL(ctx context.Context, ownerID int64, url string) ([]string, error) {
	query := `SELECT auto_tagging_rules FROM bookmarks_userprofile WHERE user_id = ` + r.marker(1)
	var rules string
	if err := r.db.QueryRowContext(ctx, query, ownerID).Scan(&rules); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load auto-tagging rules: %w", err)
	}
	return AutoTags(rules, url), nil
}
