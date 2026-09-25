package importexport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type ImportResult struct {
	Total   int
	Success int
	Failed  int
}

type ImportOptions struct {
	MapPrivateFlag bool
}

func importMarker(engine string, index int) string {
	if engine == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func importMarkers(engine string, count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = importMarker(engine, index+1)
	}
	return strings.Join(values, ",")
}

// ImportNetscape applies upstream's import merge rules independently of normal
// bookmark creation. Invalid rows count as failed and do not stop later rows.
func ImportNetscape(ctx context.Context, db *sql.DB, cfg config.Config, ownerID int64, source string, options ImportOptions) (ImportResult, error) {
	parsed, err := ParseNetscape(source)
	if err != nil {
		return ImportResult{}, err
	}
	tags, err := preloadImportTags(ctx, db, cfg.DBEngine, ownerID, parsed)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{}
	seen := make(map[string]bool, len(parsed))
	for _, item := range parsed {
		result.Total++
		if seen[item.Normalized] || !validImportedBookmark(item, cfg.DisableURLValidation) {
			result.Failed++
			continue
		}
		added, err := importedTimestamp(item.DateAdded)
		if err != nil {
			result.Failed++
			continue
		}
		modified := added
		if item.DateModified != "" {
			modified, err = importedTimestamp(item.DateModified)
			if err != nil {
				result.Failed++
				continue
			}
		}
		if err := importBookmark(ctx, db, cfg.DBEngine, ownerID, item, added, modified, tags, options); err != nil {
			return result, err
		}
		seen[item.Normalized] = true
		result.Success++
	}
	if !cfg.DisableBackgroundTasks {
		if err := scheduleImportMedia(ctx, db, cfg.DBEngine, ownerID); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validImportedBookmark(item NetscapeBookmark, disableURLValidation bool) bool {
	if item.Href == "" || len([]rune(item.Href)) > 2048 || len([]rune(item.Title)) > 512 {
		return false
	}
	if disableURLValidation {
		return true
	}
	parsed, err := url.Parse(item.Href)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ftp", "ftps":
		return true
	default:
		return false
	}
}

func importedTimestamp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC(), nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	for _, scale := range []int64{1, 1000, 1000000} {
		seconds := value / scale
		if seconds >= -62135596800 && seconds <= 253402300799 {
			return time.Unix(seconds, value%scale*(1000000000/scale)).UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q is out of range", raw)
}

func preloadImportTags(ctx context.Context, db *sql.DB, engine string, ownerID int64, parsed []NetscapeBookmark) (map[string]int64, error) {
	query := "SELECT id, name FROM bookmarks_tag WHERE owner_id = " + importMarker(engine, 1) + " ORDER BY id"
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, err
	}
	lower := cases.Lower(language.Und)
	tags := make(map[string]int64)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		tags[lower.String(name)] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, item := range parsed {
		for _, name := range item.TagNames {
			if len([]rune(name)) > 64 {
				continue
			}
			key := lower.String(name)
			if tags[key] != 0 {
				continue
			}
			query := "INSERT INTO bookmarks_tag (name, date_added, owner_id) VALUES (" + importMarkers(engine, 3) + ") RETURNING id"
			var id int64
			if err := db.QueryRowContext(ctx, query, name, time.Now().UTC(), ownerID).Scan(&id); err != nil {
				return nil, err
			}
			tags[key] = id
		}
	}
	return tags, nil
}

func importBookmark(ctx context.Context, db *sql.DB, engine string, ownerID int64, item NetscapeBookmark, added, modified time.Time, tags map[string]int64, options ImportOptions) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT id, title, description, notes, shared, is_archived FROM bookmarks_bookmark
		WHERE owner_id = ` + importMarker(engine, 1) + ` AND url_normalized = ` + importMarker(engine, 2) + ` ORDER BY id LIMIT 1`
	var id int64
	var title, description, notes string
	var shared, archived bool
	err = tx.QueryRowContext(ctx, query, ownerID, item.Normalized).Scan(&id, &title, &description, &notes, &shared, &archived)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return err
	}
	if item.Title != "" {
		title = item.Title
	}
	if item.Description != "" {
		description = item.Description
	}
	if item.Notes != "" {
		notes = item.Notes
	}
	if options.MapPrivateFlag && !item.Private {
		shared = true
	}
	// Upstream's bulk_update omits is_archived for existing bookmarks.
	if created && item.Archived {
		archived = true
	}
	if created {
		query = `INSERT INTO bookmarks_bookmark
			(url, url_normalized, title, description, notes, website_title, website_description,
			 unread, is_archived, shared, date_added, date_modified, date_accessed, owner_id,
			 web_archive_snapshot_url, favicon_file, preview_image_file, latest_snapshot_id)
			 VALUES (` + importMarkers(engine, 18) + `) RETURNING id`
		err = tx.QueryRowContext(ctx, query, item.Href, item.Normalized, title, description, notes, nil, nil,
			item.ToRead, archived, shared, added, modified, nil, ownerID, "", "", "", nil).Scan(&id)
	} else {
		query = `UPDATE bookmarks_bookmark SET url = ` + importMarker(engine, 1) + `, url_normalized = ` + importMarker(engine, 2) +
			`, title = ` + importMarker(engine, 3) + `, description = ` + importMarker(engine, 4) + `, notes = ` + importMarker(engine, 5) +
			`, unread = ` + importMarker(engine, 6) + `, shared = ` + importMarker(engine, 7) + `, is_archived = ` + importMarker(engine, 8) +
			`, date_added = ` + importMarker(engine, 9) + `, date_modified = ` + importMarker(engine, 10) +
			` WHERE id = ` + importMarker(engine, 11) + ` AND owner_id = ` + importMarker(engine, 12)
		_, err = tx.ExecContext(ctx, query, item.Href, item.Normalized, title, description, notes, item.ToRead, shared, archived, added, modified, id, ownerID)
	}
	if err != nil {
		return err
	}
	lower := cases.Lower(language.Und)
	for _, name := range item.TagNames {
		tagID := tags[lower.String(name)]
		if tagID == 0 {
			continue
		}
		query = `INSERT INTO bookmarks_bookmark_tags (bookmark_id, tag_id) VALUES (` + importMarkers(engine, 2) + `) ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, query, id, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scheduleImportMedia(ctx context.Context, db *sql.DB, engine string, ownerID int64) error {
	query := "SELECT enable_favicons, enable_preview_images FROM bookmarks_userprofile WHERE user_id = " + importMarker(engine, 1)
	var favicons, previews bool
	if err := db.QueryRowContext(ctx, query, ownerID).Scan(&favicons, &previews); err != nil {
		return err
	}
	return jobs.EnqueueMissingMedia(ctx, db, engine, ownerID, favicons, previews)
}
