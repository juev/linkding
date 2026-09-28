package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (r *Repository) FindExisting(ctx context.Context, ownerID int64, url string) (Bookmark, error) {
	query, args := r.existingURLLookup(ownerID, url)
	var id int64
	var storedURL string
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&id, &storedURL); err != nil {
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
