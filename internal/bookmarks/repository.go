package bookmarks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Bookmark struct {
	ID                    int64
	OwnerID               int64
	URL                   string
	URLNormalized         string
	Title                 string
	Description           string
	Notes                 string
	Unread                bool
	Shared                bool
	IsArchived            bool
	DateAdded             time.Time
	DateModified          time.Time
	TagNames              []string
	WebArchiveSnapshotURL string
	FaviconFile           string
	PreviewImageFile      string
}

type CreateInput struct {
	URL                 string
	Title               string
	Description         string
	Notes               string
	Unread              bool
	Shared              bool
	IsArchived          bool
	TagNames            []string
	DateAdded           *time.Time
	DateModified        *time.Time
	DisableHTMLSnapshot bool
}

type Repository struct {
	db         *sql.DB
	engine     string
	taskPolicy *TaskPolicy
}

func NewRepository(db *sql.DB, engine string) *Repository { return &Repository{db: db, engine: engine} }

func NewRepositoryWithTasks(db *sql.DB, engine string, policy TaskPolicy) *Repository {
	return &Repository{db: db, engine: engine, taskPolicy: &policy}
}

func (r *Repository) marker(n int) string {
	if r.engine == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// CreateOrUpdateData persists the bookmark and tags using the v1.47.0 duplicate
// rule. Callers must validate the URL. A task-aware repository enqueues effects
// in the same transaction as the bookmark and its tags.
func (r *Repository) CreateOrUpdateData(ctx context.Context, ownerID int64, input CreateInput) (Bookmark, bool, error) {
	if input.URL == "" || len([]rune(input.URL)) > 2048 || len([]rune(input.Title)) > 512 {
		return Bookmark{}, false, fmt.Errorf("bookmark URL or title length is invalid")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, false, fmt.Errorf("begin bookmark write: %w", err)
	}
	defer tx.Rollback()
	query := `SELECT id, url FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) +
		` AND (url_normalized = ` + r.marker(2) + ` OR (url_normalized = '' AND url = ` + r.marker(3) + `)) ORDER BY id LIMIT 1`
	var id int64
	bookmarkURL := input.URL
	err = tx.QueryRowContext(ctx, query, ownerID, NormalizeURL(input.URL), input.URL).Scan(&id, &bookmarkURL)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return Bookmark{}, false, fmt.Errorf("find existing bookmark: %w", err)
	}
	if created {
		dateAdded := time.Now().UTC()
		if input.DateAdded != nil {
			dateAdded = input.DateAdded.UTC()
		}
		dateModified := time.Now().UTC()
		if input.DateModified != nil {
			dateModified = input.DateModified.UTC()
		}
		query = `INSERT INTO bookmarks_bookmark
			(url, url_normalized, title, description, notes, website_title, website_description,
			 unread, is_archived, shared, date_added, date_modified, date_accessed, owner_id,
			 web_archive_snapshot_url, favicon_file, preview_image_file, latest_snapshot_id)
			VALUES (` + placeholders(r.engine, 18) + `) RETURNING id`
		err = tx.QueryRowContext(ctx, query, input.URL, NormalizeURL(input.URL), input.Title,
			input.Description, input.Notes, nil, nil, input.Unread, input.IsArchived, input.Shared,
			dateAdded, dateModified, nil, ownerID, "", "", "", nil).Scan(&id)
		if err != nil {
			return Bookmark{}, false, fmt.Errorf("insert bookmark: %w", err)
		}
	} else {
		// Upstream duplicate create merges only these five fields. It keeps the
		// original URL, archive state, and date_added, then stamps date_modified.
		query = `UPDATE bookmarks_bookmark SET title = ` + r.marker(1) + `, description = ` + r.marker(2) +
			`, notes = ` + r.marker(3) + `, unread = ` + r.marker(4) + `, shared = ` + r.marker(5) +
			`, date_modified = ` + r.marker(6) + ` WHERE id = ` + r.marker(7) + ` AND owner_id = ` + r.marker(8)
		if _, err := tx.ExecContext(ctx, query, input.Title, input.Description, input.Notes, input.Unread, input.Shared, time.Now().UTC(), id, ownerID); err != nil {
			return Bookmark{}, false, fmt.Errorf("update existing bookmark: %w", err)
		}
	}
	var autoTaggingRules string
	query = `SELECT auto_tagging_rules FROM bookmarks_userprofile WHERE user_id = ` + r.marker(1)
	if err := tx.QueryRowContext(ctx, query, ownerID).Scan(&autoTaggingRules); err != nil {
		return Bookmark{}, false, fmt.Errorf("load auto-tagging rules: %w", err)
	}
	tagNames := append(append([]string{}, input.TagNames...), AutoTags(autoTaggingRules, bookmarkURL)...)
	if err := r.replaceTags(ctx, tx, ownerID, id, tagNames); err != nil {
		return Bookmark{}, false, err
	}
	if err := r.enqueueTaskEffects(ctx, tx, ownerID, id, created, false, input.DisableHTMLSnapshot); err != nil {
		return Bookmark{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Bookmark{}, false, fmt.Errorf("commit bookmark: %w", err)
	}
	bookmark, err := r.GetByID(ctx, ownerID, id)
	return bookmark, created, err
}

func (r *Repository) replaceTags(ctx context.Context, tx *sql.Tx, ownerID, bookmarkID int64, input []string) error {
	names := ParseTagString(strings.Join(input, ","), ",")
	if _, err := tx.ExecContext(ctx, `DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id = `+r.marker(1), bookmarkID); err != nil {
		return fmt.Errorf("clear bookmark tags: %w", err)
	}
	for _, name := range names {
		var tagID int64
		var query string
		if r.engine == "postgres" {
			query = `SELECT id FROM bookmarks_tag WHERE owner_id = $1 AND lower(name) = lower($2) ORDER BY id LIMIT 1`
		} else {
			query = `SELECT id FROM bookmarks_tag WHERE owner_id = ? AND ld_ci_equal(name, ?) = 1 ORDER BY id LIMIT 1`
		}
		err := tx.QueryRowContext(ctx, query, ownerID, name).Scan(&tagID)
		if errors.Is(err, sql.ErrNoRows) {
			query = `INSERT INTO bookmarks_tag (name, date_added, owner_id) VALUES (` + placeholders(r.engine, 3) + `) RETURNING id`
			err = tx.QueryRowContext(ctx, query, name, time.Now().UTC(), ownerID).Scan(&tagID)
		}
		if err != nil {
			return fmt.Errorf("resolve tag %q: %w", name, err)
		}
		query = `INSERT INTO bookmarks_bookmark_tags (bookmark_id, tag_id) VALUES (` + placeholders(r.engine, 2) + `)`
		if _, err := tx.ExecContext(ctx, query, bookmarkID, tagID); err != nil {
			return fmt.Errorf("attach tag %q: %w", name, err)
		}
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, ownerID, id int64) (Bookmark, error) {
	query := `SELECT id, owner_id, url, url_normalized, title, description, notes, unread, shared,
		is_archived, date_added, date_modified, web_archive_snapshot_url, favicon_file, preview_image_file
		FROM bookmarks_bookmark WHERE owner_id = ` + r.marker(1) + ` AND id = ` + r.marker(2)
	var b Bookmark
	if err := r.db.QueryRowContext(ctx, query, ownerID, id).Scan(&b.ID, &b.OwnerID, &b.URL, &b.URLNormalized,
		&b.Title, &b.Description, &b.Notes, &b.Unread, &b.Shared, &b.IsArchived, &b.DateAdded, &b.DateModified,
		&b.WebArchiveSnapshotURL, &b.FaviconFile, &b.PreviewImageFile); err != nil {
		return Bookmark{}, err
	}
	query = `SELECT t.name FROM bookmarks_tag AS t JOIN bookmarks_bookmark_tags AS bt ON bt.tag_id = t.id
		WHERE bt.bookmark_id = ` + r.marker(1)
	rows, err := r.db.QueryContext(ctx, query, id)
	if err != nil {
		return Bookmark{}, fmt.Errorf("load bookmark tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return Bookmark{}, err
		}
		b.TagNames = append(b.TagNames, name)
	}
	if err := rows.Err(); err != nil {
		return Bookmark{}, err
	}
	slices.Sort(b.TagNames) // Python's sorted(names) uses code point order.
	return b, nil
}

func placeholders(engine string, n int) string {
	values := make([]string, n)
	for i := range values {
		if engine == "postgres" {
			values[i] = fmt.Sprintf("$%d", i+1)
		} else {
			values[i] = "?"
		}
	}
	return strings.Join(values, ", ")
}
