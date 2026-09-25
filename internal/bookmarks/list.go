package bookmarks

import (
	"context"
)

// ListPage returns the default v1.47.0 active/archived listing order.
func (r *Repository) ListPage(ctx context.Context, ownerID int64, archived bool, limit, offset int) ([]Bookmark, int64, error) {
	return r.ListFiltered(ctx, ownerID, ListOptions{Archived: archived, Limit: limit, Offset: offset})
}
