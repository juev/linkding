package importexport

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/juev/linkding/internal/bookmarks"
)

// LoadForExport includes archived bookmarks and keeps the source database's
// primary-key order used by the settings export view.
func LoadForExport(ctx context.Context, db *sql.DB, engine string, ownerID int64) ([]bookmarks.Bookmark, error) {
	query := "SELECT id FROM bookmarks_bookmark WHERE owner_id = " + importMarker(engine, 1) + " ORDER BY id"
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	repo := bookmarks.NewRepository(db, engine)
	result := make([]bookmarks.Bookmark, 0, len(ids))
	for _, id := range ids {
		item, err := repo.GetByID(ctx, ownerID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

// ExportNetscape reproduces the v1.47.0 Netscape HTML export format.
func ExportNetscape(items []bookmarks.Bookmark) string {
	lines := []string{
		"<!DOCTYPE NETSCAPE-Bookmark-file-1>",
		`<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">`,
		"<TITLE>Bookmarks</TITLE>",
		"<H1>Bookmarks</H1>",
		"<DL><p>",
	}
	for _, item := range items {
		title := item.Title
		if title == "" {
			title = item.URL
		}
		tags := slices.Clone(item.TagNames)
		slices.Sort(tags)
		if item.IsArchived {
			tags = append(tags, "linkding:bookmarks.archived")
		}
		for index := range tags {
			tags[index] = pythonHTMLEscape(tags[index])
		}
		private, toRead := "1", "0"
		if item.Shared {
			private = "0"
		}
		if item.Unread {
			toRead = "1"
		}
		lines = append(lines, fmt.Sprintf(`<DT><A HREF="%s" ADD_DATE="%d" LAST_MODIFIED="%d" PRIVATE="%s" TOREAD="%s" TAGS="%s">%s</A>`,
			item.URL, item.DateAdded.Unix(), item.DateModified.Unix(), private, toRead, strings.Join(tags, ","), pythonHTMLEscape(title)))
		description := pythonHTMLEscape(item.Description)
		if item.Notes != "" {
			description += "[linkding-notes]" + pythonHTMLEscape(item.Notes) + "[/linkding-notes]"
		}
		if description != "" {
			lines = append(lines, "<DD>"+description)
		}
	}
	lines = append(lines, "</DL><p>")
	return strings.Join(lines, "\n\r")
}

func pythonHTMLEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;").Replace(value)
}
