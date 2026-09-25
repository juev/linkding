package httpserver

import "maps"

func adminBookmarkRepr(title, url string) string {
	if title == "" {
		title = url
	}
	runes := []rune(url)
	if len(runes) > 30 {
		runes = runes[:30]
	}
	return title + " (" + string(runes) + "...)"
}

func adminBookmarkChangedFields(before, after adminBookmarkData) []string {
	var changed []string
	for _, field := range []struct {
		label string
		dirty bool
	}{
		{"Url", before.URL != after.URL},
		{"Url normalized", before.URLNormalized != after.URLNormalized},
		{"Title", before.BookmarkTitle != after.BookmarkTitle},
		{"Description", before.Description != after.Description},
		{"Notes", before.Notes != after.Notes},
		{"Website title", before.WebsiteTitle != after.WebsiteTitle},
		{"Website description", before.WebsiteDescription != after.WebsiteDescription},
		{"Web archive snapshot url", before.WebArchiveURL != after.WebArchiveURL},
		{"Favicon file", before.FaviconFile != after.FaviconFile},
		{"Preview image file", before.PreviewImageFile != after.PreviewImageFile},
		{"Unread", before.Unread != after.Unread},
		{"Is archived", before.Archived != after.Archived},
		{"Shared", before.Shared != after.Shared},
		{"Date added", before.DateAddedDate != after.DateAddedDate || before.DateAddedTime != after.DateAddedTime},
		{"Date modified", before.DateModifiedDate != after.DateModifiedDate || before.DateModifiedTime != after.DateModifiedTime},
		{"Date accessed", before.DateAccessedDate != after.DateAccessedDate || before.DateAccessedTime != after.DateAccessedTime},
		{"Owner", before.OwnerID != after.OwnerID},
		{"Tags", !maps.Equal(before.SelectedTagIDs, after.SelectedTagIDs)},
		{"Latest snapshot", before.LatestSnapshotID != after.LatestSnapshotID},
	} {
		if field.dirty {
			changed = append(changed, field.label)
		}
	}
	return changed
}
