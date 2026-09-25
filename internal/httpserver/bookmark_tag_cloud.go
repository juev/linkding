package httpserver

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/search"
)

func loadSelectableTagNames(r *http.Request, db *sql.DB, cfg config.Config, userID int64, shared bool, username string) ([]string, error) {
	var query string
	var args []any
	if shared {
		if username != "" {
			var id int64
			err := db.QueryRowContext(r.Context(), `SELECT id FROM auth_user WHERE username = `+assetMarker(cfg.DBEngine, 1), username).Scan(&id)
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			if err == sql.ErrNoRows {
				username = ""
			}
		}
		query = `SELECT DISTINCT t.name FROM bookmarks_tag t
			JOIN bookmarks_bookmark_tags bt ON bt.tag_id = t.id
			JOIN bookmarks_bookmark b ON b.id = bt.bookmark_id
			JOIN bookmarks_userprofile p ON p.user_id = b.owner_id
			JOIN auth_user u ON u.id = b.owner_id
			WHERE b.shared = ` + assetMarker(cfg.DBEngine, 1) + ` AND p.enable_sharing = ` + assetMarker(cfg.DBEngine, 2)
		args = append(args, true, true)
		if userID == 0 {
			query += ` AND p.enable_public_sharing = ` + assetMarker(cfg.DBEngine, len(args)+1)
			args = append(args, true)
		}
		if username != "" {
			query += ` AND u.username = ` + assetMarker(cfg.DBEngine, len(args)+1)
			args = append(args, username)
		}
	} else {
		query = `SELECT name FROM bookmarks_tag WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1)
		args = append(args, userID)
	}
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func buildListTagCloud(matching, available, selectedNames []string, raw string, values url.Values, profile url.Values) ([]listTag, []listTagGroup) {
	bySelectedName := make(map[string]bool, len(selectedNames))
	for _, name := range selectedNames {
		bySelectedName[strings.ToLower(name)] = true
	}
	lax, legacy := profile.Get("tag_search") == "lax", profile.Get("legacy_search") != ""
	var selected []listTag
	seen := make(map[string]bool)
	for _, name := range available {
		key := strings.ToLower(name)
		if !bySelectedName[key] || seen[key] {
			continue
		}
		seen[key] = true
		selected = append(selected, listTag{Name: name, Query: template.URL(orderedListQuery(raw, "q", search.StripTag(values.Get("q"), name, lax, legacy), "details", "page"))})
	}
	sort.Slice(selected, func(i, j int) bool { return strings.ToLower(selected[i].Name) < strings.ToLower(selected[j].Name) })

	var unselected []string
	seen = make(map[string]bool)
	for _, name := range matching {
		key := strings.ToLower(name)
		if bySelectedName[key] || seen[key] {
			continue
		}
		seen[key] = true
		unselected = append(unselected, name)
	}
	sort.Slice(unselected, func(i, j int) bool { return strings.ToLower(unselected[i]) < strings.ToLower(unselected[j]) })
	if len(unselected) == 0 {
		return selected, nil
	}
	var groups []listTagGroup
	var cjk listTagGroup
	lastChar := ""
	grouping := profile.Get("tag_grouping") != "disabled"
	for _, name := range unselected {
		first, size := utf8.DecodeRuneInString(name)
		char := strings.ToLower(string(first))
		item := listTag{Name: name, Query: addedTagQuery(raw, values.Get("q"), name, legacy)}
		if !grouping {
			if len(groups) == 0 {
				groups = append(groups, listTagGroup{})
			}
			groups[0].Tags = append(groups[0].Tags, item)
			continue
		}
		item.FirstChar, item.Remaining = name[:size], name[size:]
		if first >= '\u4e00' && first <= '\u9fff' {
			item.Highlight = len(cjk.Tags) == 0
			cjk.Tags = append(cjk.Tags, item)
			continue
		}
		if len(groups) == 0 || lastChar != char {
			groups = append(groups, listTagGroup{})
			lastChar = char
			item.Highlight = true
		}
		groups[len(groups)-1].Tags = append(groups[len(groups)-1].Tags, item)
	}
	if len(cjk.Tags) > 0 {
		groups = append(groups, cjk)
	}
	return selected, groups
}

func addedTagQuery(raw, query, name string, legacy bool) template.URL {
	if !legacy && search.IsTopLevelOr(query) {
		query = "(" + query + ")"
	}
	return template.URL(orderedListQuery(raw, "q", strings.TrimSpace(query+" #"+name), "details", "page"))
}
