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
	Name, FirstChar, Remaining string
	Query                      template.URL
	Highlight                  bool
}
type listTagGroup struct {
	Tags []listTag
}
type listBundle struct {
	ID       int64
	Name     string
	Selected bool
}
type listHiddenField struct {
	Name, Value string
}
type listPageLink struct {
	Number   int
	URL      string
	Active   bool
	Ellipsis bool
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
	PageLinks                                                                                                                                                                                                                                []listPageLink
	SelectedTags                                                                                                                                                                                                                             []listTag
	TagGroups                                                                                                                                                                                                                                []listTagGroup
	Bundles                                                                                                                                                                                                                                  []listBundle
	UserNames                                                                                                                                                                                                                                []string
	UserFilter                                                                                                                                                                                                                               string
	UserFormHidden                                                                                                                                                                                                                           []listHiddenField
	Global                                                                                                                                                                                                                                   settings.Global
	HasSnapshots                                                                                                                                                                                                                             bool
	Details                                                                                                                                                                                                                                  template.HTML
	ToastHTML                                                                                                                                                                                                                                template.HTML
}

func serveBookmarkList(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	shared := strings.HasSuffix(path, "/shared")
	archived := strings.HasSuffix(path, "/archived")
	var user auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		user, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if !shared && user.ID == 0 {
		redirectToLogin(w, r, cfg)
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
	if shared {
		data.UserFilter = values.Get("user")
		data.UserNames, err = repo.ListSharedOwnerNames(r.Context(), user.ID, user.ID != 0, opts)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		for _, name := range []string{"q", "bundle", "sort", "shared", "unread", "modified_since", "added_since"} {
			value := values.Get(name)
			if value != "" && value != map[string]string{"sort": "added_desc", "shared": "off", "unread": "off"}[name] {
				data.UserFormHidden = append(data.UserFormHidden, listHiddenField{Name: name, Value: value})
			}
		}
	}
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
	data.ReturnURL = path
	if encoded := orderedListQuery(r.URL.RawQuery, "", "", "details"); encoded != "" {
		data.ReturnURL += "?" + encoded
	}
	data.Pages = int((total + int64(limit) - 1) / int64(limit))
	if data.Pages < 1 {
		data.Pages = 1
	}
	if page > data.Pages {
		page = data.Pages
		data.Page = page
		opts.Offset = (page - 1) * limit
		if shared {
			items, _, err = repo.ListShared(r.Context(), user.ID, user.ID != 0, opts)
		} else {
			items, _, err = repo.ListFiltered(r.Context(), user.ID, opts)
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	data.HasPrevious = page > 1
	data.HasNext = page < data.Pages
	if data.HasPrevious {
		data.PreviousURL = path + "?" + pageQuery(r.URL.RawQuery, page-1)
	}
	if data.HasNext {
		data.NextURL = path + "?" + pageQuery(r.URL.RawQuery, page+1)
	}
	for _, number := range visiblePageNumbers(page, data.Pages) {
		if number == -1 {
			data.PageLinks = append(data.PageLinks, listPageLink{Ellipsis: true})
		} else {
			data.PageLinks = append(data.PageLinks, listPageLink{Number: number, URL: path + "?" + pageQuery(r.URL.RawQuery, number), Active: number == page})
		}
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
			entry.Date = absoluteBookmarkDate(item.DateAdded, time.Now())
		default:
			entry.Date = relativeBookmarkDate(item.DateAdded, time.Now())
		}
		entry.SnapshotURL = item.WebArchiveSnapshotURL
		if entry.SnapshotURL == "" {
			entry.SnapshotURL = "https://web.archive.org/web/" + item.DateAdded.UTC().Format("20060102150405") + "/" + item.URL
		}
		entry.DetailsURL = path + "?" + orderedListQuery(r.URL.RawQuery, "details", strconv.FormatInt(item.ID, 10), "page")
		entry.EditURL = cfg.URLPrefix() + "bookmarks/" + strconv.FormatInt(item.ID, 10) + "/edit?return_url=" + djangoURLQuote(data.ReturnURL)
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
			entry.Tags = append(entry.Tags, listTag{Name: name, Query: addedTagQuery(r.URL.RawQuery, data.Query, name, profile.Get("legacy_search") != "")})
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
	matchingTagNames, err := repo.ListTagNamesForSearch(r.Context(), user.ID, user.ID != 0, shared, opts)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	selectedNames := search.TagNames(data.Query, profile.Get("tag_search") == "lax")
	var availableTagNames []string
	if len(selectedNames) > 0 {
		availableTagNames, err = loadSelectableTagNames(r, db, cfg, user.ID, shared, r.URL.Query().Get("user"))
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	data.SelectedTags, data.TagGroups = buildListTagCloud(matchingTagNames, availableTagNames, selectedNames, r.URL.RawQuery, r.URL.Query(), profile)
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
	if strings.Contains(string(data.Details), "<ld-details-modal ") {
		data.PageTitle = "Bookmark details - Linkding"
	}
	integrationHeaders(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	if r.Header.Get("Turbo-Frame") == "details-modal" {
		_, _ = w.Write([]byte(`<html lang="en"><head><title>` + data.PageTitle + `</title></head><body>`))
		_, _ = w.Write([]byte(data.Details))
		_, _ = w.Write([]byte("</body></html>"))
		return
	}
	_ = bookmarkListTemplate.Execute(w, data)
}

func defaultListProfile() url.Values {
	return url.Values{"theme": {"auto"}, "items_per_page": {"30"}, "bookmark_link_target": {"_blank"}, "bookmark_description_display": {"inline"}, "bookmark_description_max_lines": {"1"}, "bookmark_date_display": {"relative"}, "tag_grouping": {"alphabetical"}, "display_view_bookmark_action": {"on"}}
}
func pageQuery(raw string, page int) string {
	return orderedListQuery(raw, "page", strconv.Itoa(page), "details")
}

// Django's QueryDict retains the first occurrence of each key when encoding.
func orderedListQuery(raw, name, value string, removed ...string) string {
	values, _ := url.ParseQuery(raw)
	var keys []string
	seen := make(map[string]bool)
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		encodedKey, _, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(encodedKey)
		if err == nil && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for _, key := range removed {
		values.Del(key)
	}
	if name != "" {
		values.Set(name, value)
		if !seen[name] {
			keys = append(keys, name)
		}
	}
	var pairs []string
	for _, key := range keys {
		for _, current := range values[key] {
			pairs = append(pairs, url.QueryEscape(key)+"="+url.QueryEscape(current))
		}
	}
	return strings.Join(pairs, "&")
}
func djangoURLQuote(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(value), "%2F", "/"), "+", "%20")
}

func visiblePageNumbers(current, pages int) []int {
	visible := map[int]bool{1: true, pages: true}
	for number := max(1, current-2); number <= min(pages, current+2); number++ {
		visible[number] = true
	}
	numbers := make([]int, 0, len(visible))
	for number := range visible {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	result := make([]int, 0, len(numbers)+2)
	for _, number := range numbers {
		if len(result) > 0 && result[len(result)-1] < number-1 {
			result = append(result, -1)
		}
		result = append(result, number)
	}
	return result
}

func bookmarkDateDelta(value, now time.Time) (years, months, weeks int) {
	value, now = value.UTC(), now.UTC()
	years = now.Year() - value.Year()
	if now.Month() < value.Month() || now.Month() == value.Month() && now.Day() < value.Day() {
		years--
	}
	months = (now.Year()-value.Year())*12 + int(now.Month()-value.Month())
	if now.Day() < value.Day() {
		months--
	}
	weeks = int(now.Sub(value).Hours()/24) / 7
	return max(0, years), max(0, months), max(0, weeks)
}

func relativeBookmarkDate(value, now time.Time) string {
	value, now = value.UTC(), now.UTC()
	years, months, weeks := bookmarkDateDelta(value, now)
	switch {
	case years > 0:
		return strconv.Itoa(years) + pluralizedDateUnit(years, "year")
	case months > 0:
		return strconv.Itoa(months) + pluralizedDateUnit(months, "month")
	case weeks > 0:
		return strconv.Itoa(weeks) + pluralizedDateUnit(weeks, "week")
	default:
		return recentBookmarkDate(value, now)
	}
}

func absoluteBookmarkDate(value, now time.Time) string {
	value, now = value.UTC(), now.UTC()
	years, months, weeks := bookmarkDateDelta(value, now)
	if years > 0 || months > 0 || weeks > 0 {
		return value.Format("01/02/2006")
	}
	return recentBookmarkDate(value, now)
}

func pluralizedDateUnit(count int, unit string) string {
	if count == 1 {
		return " " + unit + " ago"
	}
	return " " + unit + "s ago"
}

func recentBookmarkDate(value, now time.Time) string {
	if value.Day() == now.Day() {
		return "Today"
	}
	if value.Day() == now.AddDate(0, 0, -1).Day() {
		return "Yesterday"
	}
	return value.Weekday().String()
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
