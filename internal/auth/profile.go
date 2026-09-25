package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Profile contains the fields exposed by UserProfileSerializer in v1.47.0.
type Profile struct {
	Theme                 string          `json:"theme"`
	BookmarkDateDisplay   string          `json:"bookmark_date_display"`
	BookmarkLinkTarget    string          `json:"bookmark_link_target"`
	WebArchiveIntegration string          `json:"web_archive_integration"`
	TagSearch             string          `json:"tag_search"`
	EnableSharing         bool            `json:"enable_sharing"`
	EnablePublicSharing   bool            `json:"enable_public_sharing"`
	EnableFavicons        bool            `json:"enable_favicons"`
	DisplayURL            bool            `json:"display_url"`
	PermanentNotes        bool            `json:"permanent_notes"`
	SearchPreferences     json.RawMessage `json:"search_preferences"`
	Version               string          `json:"version"`
}

func (r *Repository) GetProfile(ctx context.Context, userID int64) (Profile, error) {
	query := `SELECT theme, bookmark_date_display, bookmark_link_target, web_archive_integration,
		tag_search, enable_sharing, enable_public_sharing, enable_favicons, display_url,
		permanent_notes, search_preferences FROM bookmarks_userprofile WHERE user_id = ` + r.marker(1)
	var p Profile
	var searchPreferences string
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&p.Theme, &p.BookmarkDateDisplay, &p.BookmarkLinkTarget,
		&p.WebArchiveIntegration, &p.TagSearch, &p.EnableSharing, &p.EnablePublicSharing,
		&p.EnableFavicons, &p.DisplayURL, &p.PermanentNotes, &searchPreferences)
	if err == sql.ErrNoRows {
		return Profile{}, err
	}
	if err != nil {
		return Profile{}, fmt.Errorf("load user profile: %w", err)
	}
	if !json.Valid([]byte(searchPreferences)) {
		return Profile{}, fmt.Errorf("invalid search preferences for user %d", userID)
	}
	p.SearchPreferences = json.RawMessage(searchPreferences)
	p.Version = "1.47.0"
	return p, nil
}
