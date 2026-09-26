package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/search"
)

type ListOptions struct {
	Archived      bool
	Query         string
	Sort          string
	Shared        string
	Unread        string
	Bundle        string
	User          string
	ModifiedSince string
	AddedSince    string
	Limit         int
	Offset        int
}

type bookmarkRef struct{ id, ownerID int64 }

func ListOptionsFromValues(values url.Values, archived bool, limit, offset int) ListOptions {
	return ListOptions{
		Archived: archived, Query: values.Get("q"), Sort: values.Get("sort"),
		Shared: values.Get("shared"), Unread: values.Get("unread"),
		Bundle: values.Get("bundle"), User: values.Get("user"), ModifiedSince: values.Get("modified_since"),
		AddedSince: values.Get("added_since"), Limit: limit, Offset: offset,
	}
}

type bookmarkFilter struct {
	engine string
	args   []any
}

func (f *bookmarkFilter) bind(value any) string {
	f.args = append(f.args, value)
	if f.engine == "postgres" {
		return fmt.Sprintf("$%d", len(f.args))
	}
	return "?"
}

func (f *bookmarkFilter) exactTag(name string) string {
	marker := f.bind(name)
	equality := "ld_ci_equal(t.name, " + marker + ") = 1"
	if f.engine == "postgres" {
		equality = "UPPER(t.name) = UPPER(" + marker + ")"
	}
	return "EXISTS (SELECT 1 FROM bookmarks_bookmark_tags bt JOIN bookmarks_tag t ON t.id = bt.tag_id WHERE bt.bookmark_id = b.id AND " + equality + ")"
}

func (f *bookmarkFilter) contains(field, term string) string {
	if f.engine == "postgres" {
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term)
		return "UPPER(" + field + "::text) LIKE UPPER(" + f.bind("%"+escaped+"%") + `) ESCAPE '\'`
	}
	return "ld_ci_contains(" + field + ", " + f.bind(term) + ") = 1"
}

func (f *bookmarkFilter) term(value string, lax bool) string {
	parts := []string{
		f.contains("b.title", value), f.contains("b.description", value),
		f.contains("b.notes", value), f.contains("b.url", value),
	}
	if lax {
		parts = append(parts, f.exactTag(value))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (f *bookmarkFilter) expression(node search.Node, lax bool) string {
	condition, _ := f.expressionNode(node, lax)
	return condition
}

func (f *bookmarkFilter) expressionNode(node search.Node, lax bool) (string, bool) {
	switch n := node.(type) {
	case search.Term:
		return f.term(n.Value, lax), false
	case search.Tag:
		return f.exactTag(n.Value), false
	case search.Keyword:
		switch strings.ToLower(n.Value) {
		case "unread":
			return "b.unread = " + f.bind(true), false
		case "untagged":
			return "NOT EXISTS (SELECT 1 FROM bookmarks_bookmark_tags bt WHERE bt.bookmark_id = b.id)", false
		default:
			return "", true
		}
	case search.And:
		left, leftNeutral := f.expressionNode(n.Left, lax)
		right, rightNeutral := f.expressionNode(n.Right, lax)
		if leftNeutral {
			return right, rightNeutral
		}
		if rightNeutral {
			return left, false
		}
		return "(" + left + " AND " + right + ")", false
	case search.Or:
		left, leftNeutral := f.expressionNode(n.Left, lax)
		right, rightNeutral := f.expressionNode(n.Right, lax)
		if leftNeutral {
			return right, rightNeutral
		}
		if rightNeutral {
			return left, false
		}
		return "(" + left + " OR " + right + ")", false
	case search.Not:
		operand, neutral := f.expressionNode(n.Operand, lax)
		if neutral {
			return "", true
		}
		return "(NOT " + operand + ")", false
	default:
		return "", true
	}
}

func (f *bookmarkFilter) legacy(query string, lax bool) []string {
	var conditions []string
	seenTags := make(map[string]bool)
	for _, word := range strings.Split(strings.TrimSpace(query), " ") {
		if word == "" {
			continue
		}
		switch {
		case strings.HasPrefix(word, "#"):
			tag := word[1:]
			key := strings.ToLower(tag)
			if !seenTags[key] {
				conditions = append(conditions, f.exactTag(tag))
				seenTags[key] = true
			}
		case word == "!untagged":
			conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM bookmarks_bookmark_tags bt WHERE bt.bookmark_id = b.id)")
		case word == "!unread":
			conditions = append(conditions, "b.unread = "+f.bind(true))
		case strings.HasPrefix(word, "!"):
			// The legacy parser ignores unknown !keywords.
		default:
			conditions = append(conditions, f.term(word, lax))
		}
	}
	return conditions
}

func (f *bookmarkFilter) bundleTags(raw string) []string { return ParseTagString(raw, " ") }

func (r *Repository) ListFiltered(ctx context.Context, ownerID int64, opts ListOptions) ([]Bookmark, int64, error) {
	return r.listFiltered(ctx, ownerID, &ownerID, false, false, opts, nil)
}

// ListBundlePreview evaluates an unsaved bundle with the same filters as a saved bundle.
func (r *Repository) ListBundlePreview(ctx context.Context, ownerID int64, bundle PreviewBundle, opts ListOptions) ([]Bookmark, int64, error) {
	return r.listFiltered(ctx, ownerID, &ownerID, false, false, opts, &bundle)
}

// ListShared applies the caller's search preferences to all enabled shares.
// Anonymous visitors use the configured guest profile, or the standard profile.
func (r *Repository) ListShared(ctx context.Context, viewerID int64, authenticated bool, opts ListOptions) ([]Bookmark, int64, error) {
	profileID, ownerID, err := r.sharedSearchScope(ctx, viewerID, authenticated, opts.User)
	if err != nil {
		return nil, 0, err
	}
	return r.listFiltered(ctx, profileID, ownerID, true, !authenticated, opts, nil)
}

// ListSharedOwnerNames returns the owner choices for the shared-page user filter.
// The selected owner does not narrow the choices, matching the upstream form.
func (r *Repository) ListSharedOwnerNames(ctx context.Context, viewerID int64, authenticated bool, opts ListOptions) ([]string, error) {
	opts.User = ""
	profileID, _, err := r.sharedSearchScope(ctx, viewerID, authenticated, "")
	if err != nil {
		return nil, err
	}
	filter, where, err := r.buildListFilter(ctx, profileID, nil, true, !authenticated, opts, nil)
	if err != nil {
		return nil, err
	}
	query := "SELECT DISTINCT u.username FROM bookmarks_bookmark b JOIN auth_user u ON u.id = b.owner_id WHERE " + where
	rows, err := r.db.QueryContext(ctx, query, filter.args...)
	if err != nil {
		return nil, fmt.Errorf("list shared owners: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

func (r *Repository) sharedSearchScope(ctx context.Context, viewerID int64, authenticated bool, username string) (int64, *int64, error) {
	profileID := viewerID
	if !authenticated {
		var guestID sql.NullInt64
		err := r.db.QueryRowContext(ctx, "SELECT guest_profile_user_id FROM bookmarks_globalsettings ORDER BY id LIMIT 1").Scan(&guestID)
		if err != nil && err != sql.ErrNoRows {
			return 0, nil, fmt.Errorf("load guest profile: %w", err)
		}
		if guestID.Valid {
			profileID = guestID.Int64
		}
	}
	var ownerID *int64
	if username != "" {
		query := "SELECT id FROM auth_user WHERE username = " + r.marker(1) + " ORDER BY id LIMIT 1"
		var matched int64
		err := r.db.QueryRowContext(ctx, query, username).Scan(&matched)
		if err != nil && err != sql.ErrNoRows {
			return 0, nil, fmt.Errorf("find shared owner: %w", err)
		}
		if err == nil {
			ownerID = &matched
		}
	}
	return profileID, ownerID, nil
}

// ListTagNamesForSearch returns tags attached to all matching bookmarks, before pagination.
func (r *Repository) ListTagNamesForSearch(ctx context.Context, viewerID int64, authenticated, shared bool, opts ListOptions) ([]string, error) {
	profileID := viewerID
	ownerID := &viewerID
	if shared {
		var err error
		profileID, ownerID, err = r.sharedSearchScope(ctx, viewerID, authenticated, opts.User)
		if err != nil {
			return nil, err
		}
	}
	f, where, err := r.buildListFilter(ctx, profileID, ownerID, shared, shared && !authenticated, opts, nil)
	if err != nil {
		return nil, err
	}
	query := `SELECT DISTINCT t.name FROM bookmarks_tag t WHERE EXISTS (
		SELECT 1 FROM bookmarks_bookmark_tags bt JOIN bookmarks_bookmark b ON b.id = bt.bookmark_id
		WHERE bt.tag_id = t.id AND ` + where + `)`
	rows, err := r.db.QueryContext(ctx, query, f.args...)
	if err != nil {
		return nil, fmt.Errorf("list matching tags: %w", err)
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

// Public RSS uses the standard guest search profile even when a site-wide
// guest profile is configured for the interactive shared bookmarks page.
func (r *Repository) ListPublicFeed(ctx context.Context, opts ListOptions) ([]Bookmark, int64, error) {
	var ownerID *int64
	if opts.User != "" {
		query := "SELECT id FROM auth_user WHERE username = " + r.marker(1) + " ORDER BY id LIMIT 1"
		var matched int64
		err := r.db.QueryRowContext(ctx, query, opts.User).Scan(&matched)
		if err != nil && err != sql.ErrNoRows {
			return nil, 0, err
		}
		if err == sql.ErrNoRows {
			return []Bookmark{}, 0, nil
		}
		ownerID = &matched
	}
	return r.listFiltered(ctx, 0, ownerID, true, true, opts, nil)
}

func (r *Repository) listFiltered(ctx context.Context, profileID int64, ownerID *int64, sharedFeed, publicOnly bool, opts ListOptions, preview *PreviewBundle) ([]Bookmark, int64, error) {
	if opts.Limit < 1 || opts.Offset < 0 {
		return nil, 0, fmt.Errorf("invalid bookmark page")
	}
	f, where, err := r.buildListFilter(ctx, profileID, ownerID, sharedFeed, publicOnly, opts, preview)
	if err != nil {
		return nil, 0, err
	}
	var count int64
	if err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM bookmarks_bookmark b WHERE "+where, f.args...).Scan(&count); err != nil {
		return nil, 0, fmt.Errorf("count bookmarks: %w", err)
	}
	order := "b.date_added DESC"
	switch opts.Sort {
	case "added_asc":
		order = "b.date_added ASC"
	case "modified_asc":
		order = "b.date_modified ASC"
	case "modified_desc":
		order = "b.date_modified DESC"
	case "title_asc", "title_desc":
		direction := "ASC"
		if opts.Sort == "title_desc" {
			direction = "DESC"
		}
		order = "LOWER(CASE WHEN b.title <> '' THEN b.title ELSE b.url END) " + direction
		if r.engine == "sqlite" {
			order = "ld_lower(CASE WHEN b.title <> '' THEN b.title ELSE b.url END) COLLATE LD_ROOT " + direction
		}
	}
	query := "SELECT b.id, b.owner_id FROM bookmarks_bookmark b WHERE " + where + " ORDER BY " + order + " LIMIT " + f.bind(opts.Limit) + " OFFSET " + f.bind(opts.Offset)
	rows, err := r.db.QueryContext(ctx, query, f.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list bookmarks: %w", err)
	}
	var refs []bookmarkRef
	for rows.Next() {
		var ref bookmarkRef
		if err := rows.Scan(&ref.id, &ref.ownerID); err != nil {
			rows.Close()
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	items, err := r.loadPageBookmarks(ctx, refs)
	if err != nil {
		return nil, 0, err
	}
	return items, count, nil
}

func (r *Repository) loadPageBookmarks(ctx context.Context, refs []bookmarkRef) ([]Bookmark, error) {
	items := make([]Bookmark, len(refs))
	// Keep each IN clause below SQLite's parameter limit, even for an API limit
	// supplied by the caller rather than the default page size.
	const batchSize = 500
	for start := 0; start < len(refs); start += batchSize {
		end := min(start+batchSize, len(refs))
		batch := refs[start:end]
		args := make([]any, len(batch))
		positions := make(map[int64]int, len(batch))
		for i, ref := range batch {
			args[i] = ref.id
			positions[ref.id] = start + i
		}
		markers := placeholders(r.engine, len(batch))
		query := `SELECT ` + bookmarkSelectColumns + ` FROM bookmarks_bookmark WHERE id IN (` + markers + `)`
		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("load page bookmarks: %w", err)
		}
		for rows.Next() {
			var item Bookmark
			if err := scanBookmark(rows, &item); err != nil {
				rows.Close()
				return nil, err
			}
			position, ok := positions[item.ID]
			if !ok || item.OwnerID != refs[position].ownerID {
				rows.Close()
				return nil, fmt.Errorf("load bookmark %d: %w", item.ID, sql.ErrNoRows)
			}
			items[position] = item
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for i, ref := range batch {
			if items[start+i].ID != ref.id {
				return nil, fmt.Errorf("load bookmark %d: %w", ref.id, sql.ErrNoRows)
			}
		}
		query = `SELECT bt.bookmark_id, t.name FROM bookmarks_bookmark_tags AS bt
			JOIN bookmarks_tag AS t ON t.id = bt.tag_id WHERE bt.bookmark_id IN (` + markers + `)`
		rows, err = r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("load page bookmark tags: %w", err)
		}
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return nil, err
			}
			position, ok := positions[id]
			if !ok {
				rows.Close()
				return nil, fmt.Errorf("unexpected bookmark tag for %d", id)
			}
			items[position].TagNames = append(items[position].TagNames, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for i := start; i < end; i++ {
			slices.Sort(items[i].TagNames)
		}
	}
	return items, nil
}

func (r *Repository) buildListFilter(ctx context.Context, profileID int64, ownerID *int64, sharedFeed, publicOnly bool, opts ListOptions, preview *PreviewBundle) (*bookmarkFilter, string, error) {
	tagSearch := "strict"
	var legacy bool
	if profileID > 0 {
		profileQuery := "SELECT tag_search, legacy_search FROM bookmarks_userprofile WHERE user_id = " + r.marker(1)
		if err := r.db.QueryRowContext(ctx, profileQuery, profileID).Scan(&tagSearch, &legacy); err != nil {
			return nil, "", fmt.Errorf("load search profile: %w", err)
		}
	}
	f := &bookmarkFilter{engine: r.engine}
	conditions := make([]string, 0, 8)
	if ownerID != nil {
		conditions = append(conditions, "b.owner_id = "+f.bind(*ownerID))
	}
	if sharedFeed {
		conditions = append(conditions, "b.shared = "+f.bind(true))
		ownerPolicy := "EXISTS (SELECT 1 FROM bookmarks_userprofile op WHERE op.user_id = b.owner_id AND op.enable_sharing = " + f.bind(true)
		if publicOnly {
			ownerPolicy += " AND op.enable_public_sharing = " + f.bind(true)
		}
		conditions = append(conditions, ownerPolicy+")")
	} else {
		conditions = append(conditions, "b.is_archived = "+f.bind(opts.Archived))
	}
	if opts.Query != "" {
		if legacy {
			conditions = append(conditions, f.legacy(opts.Query, tagSearch == "lax")...)
		} else {
			node, err := search.Parse(opts.Query)
			if err != nil {
				conditions = append(conditions, "1 = 0")
			} else if node != nil {
				if condition := f.expression(node, tagSearch == "lax"); condition != "" {
					conditions = append(conditions, condition)
				}
			}
		}
	}
	switch opts.Shared {
	case "yes":
		conditions = append(conditions, "b.shared = "+f.bind(true))
	case "no":
		conditions = append(conditions, "b.shared = "+f.bind(false))
	}
	switch opts.Unread {
	case "yes":
		conditions = append(conditions, "b.unread = "+f.bind(true))
	case "no":
		conditions = append(conditions, "b.unread = "+f.bind(false))
	}
	for _, temporal := range []struct{ value, field string }{{opts.ModifiedSince, "b.date_modified"}, {opts.AddedSince, "b.date_added"}} {
		if parsed, valid := parseSearchDate(temporal.value); valid {
			conditions = append(conditions, temporal.field+" > "+f.bind(parsed))
		}
	}
	if bundleID, err := strconv.ParseInt(opts.Bundle, 10, 64); preview != nil || err == nil && bundleID > 0 && (!sharedFeed || !publicOnly) {
		bundle := PreviewBundle{}
		found := preview != nil
		if preview != nil {
			bundle = *preview
		} else {
			bundle, found, err = r.loadBundle(ctx, profileID, bundleID)
			if err != nil {
				return nil, "", err
			}
		}
		if found {
			for _, word := range strings.Split(strings.TrimSpace(bundle.Search), " ") {
				if word != "" && !strings.HasPrefix(word, "#") && !strings.HasPrefix(word, "!") {
					conditions = append(conditions, f.term(word, false))
				}
			}
			if tags := f.bundleTags(bundle.AnyTags); len(tags) > 0 {
				any := make([]string, 0, len(tags))
				for _, tag := range tags {
					any = append(any, f.exactTag(tag))
				}
				conditions = append(conditions, "("+strings.Join(any, " OR ")+")")
			}
			for _, tag := range f.bundleTags(bundle.AllTags) {
				conditions = append(conditions, f.exactTag(tag))
			}
			if tags := f.bundleTags(bundle.ExcludedTags); len(tags) > 0 {
				any := make([]string, 0, len(tags))
				for _, tag := range tags {
					any = append(any, f.exactTag(tag))
				}
				conditions = append(conditions, "NOT ("+strings.Join(any, " OR ")+")")
			}
			switch bundle.FilterUnread {
			case "yes":
				conditions = append(conditions, "b.unread = "+f.bind(true))
			case "no":
				conditions = append(conditions, "b.unread = "+f.bind(false))
			}
			switch bundle.FilterShared {
			case "yes":
				conditions = append(conditions, "b.shared = "+f.bind(true))
			case "no":
				conditions = append(conditions, "b.shared = "+f.bind(false))
			}
		}
	}
	where := strings.Join(conditions, " AND ")
	return f, where, nil
}

func parseSearchDate(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999-0700", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type PreviewBundle struct {
	Search, AnyTags, AllTags, ExcludedTags, FilterUnread, FilterShared string
}

func (r *Repository) loadBundle(ctx context.Context, ownerID, bundleID int64) (PreviewBundle, bool, error) {
	var bundle PreviewBundle
	query := "SELECT search, any_tags, all_tags, excluded_tags, filter_unread, filter_shared FROM bookmarks_bookmarkbundle WHERE id = " + r.marker(1) + " AND owner_id = " + r.marker(2)
	err := r.db.QueryRowContext(ctx, query, bundleID, ownerID).Scan(&bundle.Search, &bundle.AnyTags, &bundle.AllTags, &bundle.ExcludedTags, &bundle.FilterUnread, &bundle.FilterShared)
	if err == sql.ErrNoRows {
		return PreviewBundle{}, false, nil
	}
	if err != nil {
		return PreviewBundle{}, false, fmt.Errorf("load bundle: %w", err)
	}
	return bundle, true, nil
}
