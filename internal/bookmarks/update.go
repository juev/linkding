package bookmarks

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrDuplicateURL = errors.New("bookmark with this URL already exists")

type UpdateInput struct {
	URL         *string
	Title       *string
	Description *string
	Notes       *string
	Unread      *bool
	Shared      *bool
	IsArchived  *bool
	TagNames    *[]string
	DateAdded   *time.Time
	// The REST serializer compares URL strings; the UI form uses query_existing.
	ExactURLDuplicateCheck bool
}

func (r *Repository) UpdateData(ctx context.Context, ownerID, id int64, input UpdateInput) (Bookmark, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, err
	}
	defer tx.Rollback()
	query := `SELECT url, title, description, notes, unread, shared, is_archived, date_added
		FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) + ` AND id = ` + r.marker(2)
	if r.engine == "postgres" {
		query += ` FOR UPDATE`
	}
	var url, title, description, notes string
	var unread, shared, archived bool
	var added time.Time
	if err := tx.QueryRowContext(ctx, query, ownerID, id).Scan(&url, &title, &description, &notes, &unread, &shared, &archived, &added); err != nil {
		return Bookmark{}, err
	}
	originalURL := url
	if input.URL != nil {
		url = *input.URL
		var duplicate bool
		var args []any
		if input.ExactURLDuplicateCheck {
			query = `SELECT EXISTS(SELECT 1 FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) +
				` AND url = ` + r.marker(2) + ` AND id <> ` + r.marker(3) + `)`
			args = []any{ownerID, url, id}
		} else {
			query = `SELECT EXISTS(SELECT 1 FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) +
				` AND (url_normalized = ` + r.marker(2) + ` OR (url_normalized = '' AND url = ` + r.marker(3) +
				`)) AND id <> ` + r.marker(4) + `)`
			args = []any{ownerID, NormalizeURL(url), url, id}
		}
		if err := tx.QueryRowContext(ctx, query, args...).Scan(&duplicate); err != nil {
			return Bookmark{}, err
		}
		if duplicate {
			return Bookmark{}, ErrDuplicateURL
		}
	}
	if input.Title != nil {
		title = *input.Title
	}
	if input.Description != nil {
		description = *input.Description
	}
	if input.Notes != nil {
		notes = *input.Notes
	}
	if input.Unread != nil {
		unread = *input.Unread
	}
	if input.Shared != nil {
		shared = *input.Shared
	}
	if input.IsArchived != nil {
		archived = *input.IsArchived
	}
	if input.DateAdded != nil {
		added = input.DateAdded.UTC()
	}
	query = `UPDATE bookmarks_bookmark SET url = ` + r.marker(1) + `, url_normalized = ` + r.marker(2) +
		`, title = ` + r.marker(3) + `, description = ` + r.marker(4) + `, notes = ` + r.marker(5) +
		`, unread = ` + r.marker(6) + `, shared = ` + r.marker(7) + `, is_archived = ` + r.marker(8) +
		`, date_added = ` + r.marker(9) + `, date_modified = ` + r.marker(10) +
		` WHERE owner_id = ` + r.marker(11) + ` AND id = ` + r.marker(12)
	if _, err := tx.ExecContext(ctx, query, url, NormalizeURL(url), title, description, notes,
		unread, shared, archived, added, time.Now().UTC(), ownerID, id); err != nil {
		return Bookmark{}, fmt.Errorf("update bookmark: %w", err)
	}
	var tagNames []string
	if input.TagNames != nil {
		tagNames = *input.TagNames
	} else {
		query = `SELECT t.name FROM bookmarks_tag AS t JOIN bookmarks_bookmark_tags AS bt ON bt.tag_id = t.id
			WHERE bt.bookmark_id = ` + r.marker(1)
		rows, err := tx.QueryContext(ctx, query, id)
		if err != nil {
			return Bookmark{}, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return Bookmark{}, err
			}
			tagNames = append(tagNames, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Bookmark{}, err
		}
	}
	query = `SELECT auto_tagging_rules FROM bookmarks_userprofile WHERE user_id = ` + r.marker(1)
	var rules string
	if err := tx.QueryRowContext(ctx, query, ownerID).Scan(&rules); err != nil {
		return Bookmark{}, err
	}
	tagNames = append(tagNames, AutoTags(rules, url)...)
	if err := r.replaceTags(ctx, tx, ownerID, id, tagNames); err != nil {
		return Bookmark{}, err
	}
	if err := r.enqueueTaskEffects(ctx, tx, ownerID, id, false, url != originalURL, false); err != nil {
		return Bookmark{}, err
	}
	if err := tx.Commit(); err != nil {
		return Bookmark{}, err
	}
	return r.GetByID(ctx, ownerID, id)
}
