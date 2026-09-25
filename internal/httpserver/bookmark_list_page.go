package httpserver

import (
	"database/sql"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/markdown"
	"github.com/juev/linkding/internal/search"
	"github.com/juev/linkding/internal/settings"
)

//go:embed bookmark_list_page.html
var bookmarkListTemplateFile embed.FS
var bookmarkListTemplate = template.Must(template.ParseFS(bookmarkListTemplateFile, "bookmark_list_page.html"))

type listTag struct {
	Name  string
	Query template.URL
}
type listBundle struct {
	ID       int64
	Name     string
	Selected bool
}
type bookmarkListItem struct {
	ID                                                                                                         int64
	URL, Title, Description, Notes, Owner, Date, SnapshotURL, DetailsURL, EditURL, FaviconURL, PreviewURL, CSS string
	NotesHTML                                                                                                  template.HTML
	Tags                                                                                                       []listTag
	Editable, Archived, Unread, Shared, ShowNotes, ShowMarkRead, ShowUnshare                                   bool
}
type bookmarkListPage struct {
	Prefix, CSRFToken, Theme, CustomCSSHash, PageTitle, Heading, Action, ReturnURL, Query, Sort, SharedFilter, UnreadFilter, LinkTarget, DescriptionDisplay, SearchMode, QueryError, PreviousURL, NextURL                                    string
	CustomCSS, Authenticated, IsSuperuser, EnableSharing, ShowURL, ShowFavicons, ShowPreviews, ShowNotes, CollapseSidePanel, HideBundles, ShowView, ShowEdit, ShowArchive, ShowRemove, StickyPagination, HasNext, HasPrevious, HasQueryError bool
	DescriptionMaxLines, Page, Pages                                                                                                                                                                                                         int
	Total                                                                                                                                                                                                                                    int64
	Items                                                                                                                                                                                                                                    []bookmarkListItem
	Tags                                                                                                                                                                                                                                     []listTag
	Bundles                                                                                                                                                                                                                                  []listBundle
	Global                                                                                                                                                                                                                                   settings.Global
	HasSnapshots                                                                                                                                                                                                                             bool
	Details                                                                                                                                                                                                                                  template.HTML
	ToastHTML                                                                                                                                                                                                                                template.HTML
}

func serveBookmarkList(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	shared := strings.HasSuffix(path, "/shared")
	archived := strings.HasSuffix(path, "/archived")
	var user auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		user, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if !shared && user.ID == 0 {
		http.Redirect(w, r, cfg.URLPrefix()+"login/?next="+url.QueryEscape(path), http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		serveBookmarkSearchPost(w, r, path, cfg, db, user)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	global, err := settings.LoadGlobal(r.Context(), db)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	profile := defaultListProfile()
	profileID := user.ID
	if profileID == 0 && global.GuestProfileUserID.Valid {
		profileID = global.GuestProfileUserID.Int64
	}
	if profileID > 0 {
		profile, err = settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, profileID)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	preferences := map[string]string{}
	if profileID > 0 {
		var raw string
		query := `SELECT search_preferences FROM bookmarks_userprofile WHERE user_id = ` + assetMarker(cfg.DBEngine, 1)
		if err := db.QueryRowContext(r.Context(), query, profileID).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &preferences)
		}
	}
	values := r.URL.Query()
	for _, name := range []string{"sort", "shared", "unread"} {
		if values.Get(name) == "" && preferences[name] != "" {
			values.Set(name, preferences[name])
		}
	}
	for name, fallback := range map[string]string{"sort": "added_desc", "shared": "off", "unread": "off"} {
		if values.Get(name) == "" {
			values.Set(name, fallback)
		}
	}
	page := 1
	if parsed, err := strconv.Atoi(values.Get("page")); err == nil && parsed > 0 {
		page = parsed
	}
	limit := 30
	if parsed, err := strconv.Atoi(profile.Get("items_per_page")); err == nil && parsed > 0 {
		limit = parsed
	}
	if page > 1_000_000 {
		page = 1_000_000
	}
	opts := bookmarks.ListOptionsFromValues(values, archived, limit, (page-1)*limit)
	var items []bookmarks.Bookmark
	var total int64
	if shared {
		items, total, err = repo.ListShared(r.Context(), user.ID, user.ID != 0, opts)
	} else {
		items, total, err = repo.ListFiltered(r.Context(), user.ID, opts)
	}
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := bookmarkListPage{Prefix: cfg.URLPrefix(), Theme: profile.Get("theme"), CustomCSS: profile.Get("custom_css") != "", Authenticated: user.ID != 0, IsSuperuser: user.IsSuperuser, EnableSharing: profile.Get("enable_sharing") != "", Global: global, Page: page, Total: total, Query: values.Get("q"), Sort: values.Get("sort"), SharedFilter: values.Get("shared"), UnreadFilter: values.Get("unread"), LinkTarget: profile.Get("bookmark_link_target"), DescriptionDisplay: profile.Get("bookmark_description_display"), ShowURL: profile.Get("display_url") != "", ShowFavicons: profile.Get("enable_favicons") != "", ShowPreviews: profile.Get("enable_preview_images") != "", ShowNotes: profile.Get("permanent_notes") != "", CollapseSidePanel: profile.Get("collapse_side_panel") != "", HideBundles: profile.Get("hide_bundles") != "", ShowView: profile.Get("display_view_bookmark_action") != "", ShowEdit: profile.Get("display_edit_bookmark_action") != "", ShowArchive: profile.Get("display_archive_bookmark_action") != "", ShowRemove: profile.Get("display_remove_bookmark_action") != "", StickyPagination: profile.Get("sticky_pagination") != "", HasSnapshots: cfg.EnableSnapshots}
	if parsed, err := strconv.Atoi(profile.Get("bookmark_description_max_lines")); err == nil {
		data.DescriptionMaxLines = parsed
	}
	data.PageTitle = "Bookmarks - Linkding"
	data.Heading = "Bookmarks"
	data.Action = path + "/action"
	if shared {
		data.PageTitle = "Shared bookmarks - Linkding"
		data.Heading = "Shared bookmarks"
		data.SearchMode = "shared"
	} else if archived {
		data.PageTitle = "Archived bookmarks - Linkding"
		data.Heading = "Archived bookmarks"
		data.SearchMode = "archived"
	}
	data.ReturnURL = r.URL.RequestURI()
	data.Pages = int((total + int64(limit) - 1) / int64(limit))
	if data.Pages < 1 {
		data.Pages = 1
	}
	data.HasPrevious = page > 1
	data.HasNext = page < data.Pages
	if data.HasPrevious {
		data.PreviousURL = path + "?" + pageQuery(values, page-1)
	}
	if data.HasNext {
		data.NextURL = path + "?" + pageQuery(values, page+1)
	}
	if profile.Get("legacy_search") == "" && data.Query != "" {
		if _, parseErr := search.Parse(data.Query); parseErr != nil {
			data.HasQueryError = true
			data.QueryError = parseErr.Error()
		}
	}
	if data.CustomCSS && profileID > 0 {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), profileID).Scan(&data.CustomCSSHash)
	}
	for _, item := range items {
		entry := bookmarkListItem{ID: item.ID, URL: item.URL, Title: item.Title, Description: item.Description, Notes: item.Notes, NotesHTML: markdown.Render(item.Notes), Editable: user.ID != 0 && user.ID == item.OwnerID, Archived: item.IsArchived, Unread: item.Unread, Shared: item.Shared, ShowMarkRead: user.ID == item.OwnerID && item.Unread, ShowUnshare: user.ID == item.OwnerID && item.Shared && data.EnableSharing, ShowNotes: item.Notes != "" && !data.ShowNotes}
		if entry.Title == "" {
			entry.Title = item.URL
		}
		if item.Unread {
			entry.CSS = "unread"
		}
		if item.Shared {
			if entry.CSS != "" {
				entry.CSS += " "
			}
			entry.CSS += "shared"
		}
		switch profile.Get("bookmark_date_display") {
		case "hidden":
		case "absolute":
			entry.Date = item.DateAdded.Format("Jan 02, 2006")
		default:
			entry.Date = relativeBookmarkDate(item.DateAdded)
		}
		entry.SnapshotURL = item.WebArchiveSnapshotURL
		if entry.SnapshotURL == "" {
			entry.SnapshotURL = "https://web.archive.org/web/" + item.DateAdded.UTC().Format("20060102150405") + "/" + item.URL
		}
		detailValues := cloneQuery(r.URL.Query())
		detailValues.Del("page")
		detailValues.Set("details", strconv.FormatInt(item.ID, 10))
		entry.DetailsURL = path + "?" + detailValues.Encode()
		entry.EditURL = cfg.URLPrefix() + "bookmarks/" + strconv.FormatInt(item.ID, 10) + "/edit?return_url=" + url.QueryEscape(data.ReturnURL)
		if item.FaviconFile != "" {
			entry.FaviconURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.FaviconFile, "/")
		}
		if item.PreviewImageFile != "" {
			entry.PreviewURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.PreviewImageFile, "/")
		}
		if shared {
			if err := db.QueryRowContext(r.Context(), `SELECT username FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1), item.OwnerID).Scan(&entry.Owner); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
		}
		for _, name := range item.TagNames {
			entry.Tags = append(entry.Tags, listTag{Name: name, Query: template.URL(withQuery(r.URL.Query(), "q", strings.TrimSpace(data.Query+" #"+name)))})
		}
		data.Items = append(data.Items, entry)
	}
	if user.ID > 0 && !shared && !data.HideBundles {
		data.Bundles, err = loadListBundles(r, db, cfg, user.ID, values.Get("bundle"))
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	data.Tags, err = loadListTags(r, db, cfg, user.ID, shared, archived, values)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, data.CSRFToken, r.URL.Path)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Details, err = renderBookmarkDetails(r, cfg, db, repo, user, profile, data.CSRFToken)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	if r.Header.Get("Turbo-Frame") == "details-modal" {
		_, _ = w.Write([]byte(data.Details))
		return
	}
	_ = bookmarkListTemplate.Execute(w, data)
}

func defaultListProfile() url.Values {
	return url.Values{"theme": {"auto"}, "items_per_page": {"30"}, "bookmark_link_target": {"_blank"}, "bookmark_description_display": {"inline"}, "bookmark_description_max_lines": {"1"}, "bookmark_date_display": {"relative"}, "tag_grouping": {"alphabetical"}}
}
func withQuery(input url.Values, name, value string) string {
	copy := url.Values{}
	for key, values := range input {
		copy[key] = append([]string(nil), values...)
	}
	copy.Set(name, value)
	copy.Del("page")
	copy.Del("details")
	return copy.Encode()
}
func pageQuery(input url.Values, page int) string {
	copy := url.Values{}
	for key, values := range input {
		copy[key] = append([]string(nil), values...)
	}
	copy.Set("page", strconv.Itoa(page))
	copy.Del("details")
	return copy.Encode()
}
func relativeBookmarkDate(added time.Time) string {
	days := int(time.Since(added).Hours() / 24)
	if days < 1 {
		return "today"
	}
	if days == 1 {
		return "yesterday"
	}
	if days < 30 {
		return strconv.Itoa(days) + " days ago"
	}
	return added.Format("Jan 02, 2006")
}
func loadListBundles(r *http.Request, db *sql.DB, cfg config.Config, userID int64, selected string) ([]listBundle, error) {
	rows, err := db.QueryContext(r.Context(), `SELECT id,name FROM bookmarks_bookmarkbundle WHERE owner_id = `+assetMarker(cfg.DBEngine, 1)+` ORDER BY "order", id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []listBundle
	for rows.Next() {
		var item listBundle
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		item.Selected = strconv.FormatInt(item.ID, 10) == selected
		result = append(result, item)
	}
	return result, rows.Err()
}
func loadListTags(r *http.Request, db *sql.DB, cfg config.Config, userID int64, shared, archived bool, values url.Values) ([]listTag, error) {
	query := `SELECT DISTINCT t.name FROM bookmarks_tag t JOIN bookmarks_bookmark_tags bt ON bt.tag_id=t.id JOIN bookmarks_bookmark b ON b.id=bt.bookmark_id WHERE `
	args := []any{}
	if shared {
		query += `b.shared = ` + assetMarker(cfg.DBEngine, 1) + ` AND b.owner_id IN (SELECT user_id FROM bookmarks_userprofile WHERE enable_sharing = ` + assetMarker(cfg.DBEngine, 2)
		args = append(args, true, true)
		if userID == 0 {
			query += ` AND enable_public_sharing = ` + assetMarker(cfg.DBEngine, 3)
			args = append(args, true)
		}
		query += `)`
	} else {
		query += `b.owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` AND b.is_archived = ` + assetMarker(cfg.DBEngine, 2)
		args = append(args, userID, archived)
	}
	query += ` ORDER BY t.name`
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []listTag
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, listTag{Name: name, Query: template.URL(withQuery(values, "q", strings.TrimSpace(values.Get("q")+" #"+name)))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(tags, func(i, j int) bool { return strings.ToLower(tags[i].Name) < strings.ToLower(tags[j].Name) })
	return tags, nil
}
