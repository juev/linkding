package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/juev/linkding/internal/jobs"
)

type TaskPolicy struct {
	BackgroundDisabled bool
	SnapshotsEnabled   bool
}

type bookmarkTaskProfile struct {
	Favicons     bool
	Previews     bool
	WebArchive   string
	AutoSnapshot bool
}

func (r *Repository) enqueueTaskEffects(ctx context.Context, tx *sql.Tx, ownerID, bookmarkID int64, created, urlChanged, disableHTMLSnapshot bool) error {
	if r.taskPolicy == nil || r.taskPolicy.BackgroundDisabled {
		return nil
	}
	var profile bookmarkTaskProfile
	query := `SELECT enable_favicons, enable_preview_images, web_archive_integration,
		enable_automatic_html_snapshots FROM bookmarks_userprofile WHERE user_id = ` + r.marker(1)
	if err := tx.QueryRowContext(ctx, query, ownerID).Scan(&profile.Favicons, &profile.Previews, &profile.WebArchive, &profile.AutoSnapshot); err != nil {
		return fmt.Errorf("load bookmark task preferences: %w", err)
	}
	queue := jobs.New(r.db, r.engine)
	enqueue := func(kind string, payload any) error {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := queue.EnqueueTx(ctx, tx, kind, encoded); err != nil {
			return err
		}
		return nil
	}
	type bookmarkPayload struct {
		BookmarkID  int64 `json:"bookmark_id"`
		ForceUpdate bool  `json:"force_update,omitempty"`
	}
	basic := bookmarkPayload{BookmarkID: bookmarkID}
	if created && profile.WebArchive == "enabled" {
		if err := enqueue("web_archive_snapshot", basic); err != nil {
			return err
		}
	}
	if profile.Favicons {
		if err := enqueue("load_favicon", basic); err != nil {
			return err
		}
	}
	if profile.Previews {
		if err := enqueue("load_preview_image", basic); err != nil {
			return err
		}
	}
	if !created && urlChanged && profile.WebArchive == "enabled" {
		if err := enqueue("web_archive_snapshot", bookmarkPayload{BookmarkID: bookmarkID, ForceUpdate: true}); err != nil {
			return err
		}
	}
	if created && profile.AutoSnapshot && r.taskPolicy.SnapshotsEnabled && !disableHTMLSnapshot {
		now := time.Now().UTC()
		query := `INSERT INTO bookmarks_bookmarkasset
			(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
			VALUES (` + placeholders(r.engine, 9) + `) RETURNING id`
		var assetID int64
		if err := tx.QueryRowContext(ctx, query, now, "", nil, "snapshot", "", "New snapshot", "pending", false, bookmarkID).Scan(&assetID); err != nil {
			return fmt.Errorf("create pending snapshot: %w", err)
		}
		if err := enqueue("process_snapshot", struct {
			AssetID int64 `json:"asset_id"`
		}{assetID}); err != nil {
			return err
		}
	}
	return nil
}
