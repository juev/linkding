package httpserver

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/markdown"
	"github.com/juev/linkding/internal/settings"
)

//go:embed bundles_page.html bundles_preview.html
var bundlesUIFiles embed.FS
var bundlesPageTemplate = template.Must(template.ParseFS(bundlesUIFiles, "bundles_page.html"))
var bundlesPreviewTemplate = template.Must(template.ParseFS(bundlesUIFiles, "bundles_preview.html"))

type bundlePreviewRow struct {
	ID                                                                      int64
	URL, Title, Description, Date, SnapshotURL, FaviconURL, PreviewURL, CSS string
	NotesHTML                                                               template.HTML
	Tags                                                                    []listTag
}
type bundlePageData struct {
	Prefix, Theme, CustomCSSHash, CSRFToken, PageTitle, Heading, Action, Flash, NameError string
	CustomCSS, EnableSharing, IsSuperuser, Editor, HasPreview, Prefetch                   bool
	Bundles                                                                               []apiBundle
	Form                                                                                  apiBundle
	Preview                                                                               template.HTML
	ToastHTML                                                                             template.HTML
}
type bundlePreviewData struct {
	Count                                                                                  int64
	Page, DescriptionMaxLines                                                              int
	Prefix, LinkTarget, DescriptionDisplay, PreviousURL, NextURL                           string
	ShowURL, ShowFavicons, ShowPreviews, ShowNotes, StickyPagination, HasPrevious, HasNext bool
	Items                                                                                  []bundlePreviewRow
	PageLinks                                                                              []listPageLink
}

func serveBundlesUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository) {
	root := cfg.URLPrefix() + "bundles"
	path := r.URL.Path
	if path != root && path != root+"/action" && path != root+"/new" && path != root+"/preview" && !(strings.HasPrefix(path, root+"/") && strings.HasSuffix(path, "/edit")) {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	switch path {
	case root:
		serveBundlesIndexUI(w, r, cfg, db, user)
	case root + "/action":
		serveBundlesActionUI(w, r, cfg, db, user)
	case root + "/preview":
		serveBundlesPreviewUI(w, r, cfg, db, repo, user)
	default:
		serveBundleEditorUI(w, r, cfg, db, repo, user)
	}
}

func bundlePageBase(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) (bundlePageData, error) {
	profile, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		return bundlePageData{}, err
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			return bundlePageData{}, err
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		return bundlePageData{}, err
	}
	data := bundlePageData{Prefix: cfg.URLPrefix(), Theme: profile.Get("theme"), CustomCSS: profile.Get("custom_css") != "", EnableSharing: profile.Get("enable_sharing") != "", IsSuperuser: user.IsSuperuser, CSRFToken: masked}
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, masked, r.URL.Path)
	if err != nil {
		return bundlePageData{}, err
	}
	if data.CustomCSS {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&data.CustomCSSHash)
	}
	global, err := settings.LoadGlobal(r.Context(), db)
	if err != nil {
		return bundlePageData{}, err
	}
	data.Prefetch = global.EnableLinkPrefetch
	return data, nil
}

func renderBundlesPage(w http.ResponseWriter, r *http.Request, data bundlePageData, status int) {
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = bundlesPageTemplate.Execute(w, data)
}

func serveBundlesIndexUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", 405)
		return
	}
	data, err := bundlePageBase(w, r, cfg, db, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.PageTitle = "Bundles - Linkding"
	data.Heading = "Bundles"
	data.Flash = takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_bundle_success")
	query := `SELECT ` + bundleColumns() + ` FROM bookmarks_bookmarkbundle WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` ORDER BY "order",id`
	rows, err := db.QueryContext(r.Context(), query, user.ID)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	for rows.Next() {
		bundle, err := scanBundle(rows)
		if err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		data.Bundles = append(data.Bundles, bundle)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	renderBundlesPage(w, r, data, 200)
}

func serveBundlesActionUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		http.Error(w, "CSRF verification failed", 403)
		return
	}
	root := cfg.URLPrefix() + "bundles"
	if raw := r.PostForm.Get("remove_bundle"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		bundle, err := getBundle(r, cfg, db, user.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if err := deleteBundle(r, cfg, db, user.ID, id); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_bundle_success", "Bundle '"+bundle.Name+"' removed successfully.")
	} else if raw := r.PostForm.Get("move_bundle"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		position, err := strconv.Atoi(r.PostForm.Get("move_position"))
		if err != nil {
			http.Error(w, "Invalid position", 400)
			return
		}
		if err := moveBundleUI(r, cfg, db, user.ID, id, position); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	http.Redirect(w, r, root, http.StatusFound)
}

func moveBundleUI(r *http.Request, cfg config.Config, db *sql.DB, ownerID, id int64, position int) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), `SELECT id FROM bookmarks_bookmarkbundle WHERE owner_id = `+assetMarker(cfg.DBEngine, 1)+` ORDER BY "order",id`, ownerID)
	if err != nil {
		return err
	}
	ids := []int64{}
	old := -1
	for rows.Next() {
		var current int64
		if err := rows.Scan(&current); err != nil {
			rows.Close()
			return err
		}
		if current == id {
			old = len(ids)
		}
		ids = append(ids, current)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if old < 0 {
		return sql.ErrNoRows
	}
	ids = append(ids[:old], ids[old+1:]...)
	if position < 0 {
		position = 0
	}
	if position > len(ids) {
		position = len(ids)
	}
	ids = append(ids, 0)
	copy(ids[position+1:], ids[position:])
	ids[position] = id
	query := `UPDATE bookmarks_bookmarkbundle SET "order" = ` + assetMarker(cfg.DBEngine, 1) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 2) + ` AND owner_id = ` + assetMarker(cfg.DBEngine, 3)
	for index, current := range ids {
		if _, err := tx.ExecContext(r.Context(), query, index, current, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func serveBundleEditorUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, repo *bookmarks.Repository, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	root := cfg.URLPrefix() + "bundles"
	data, err := bundlePageBase(w, r, cfg, db, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Editor = true
	data.Form.FilterUnread = "off"
	data.Form.FilterShared = "off"
	if r.URL.Path == root+"/new" {
		data.PageTitle = "New bundle - Linkding"
		data.Heading = "New bundle"
		data.Action = root + "/new"
		if q := r.URL.Query().Get("q"); q != "" {
			for _, word := range strings.Fields(q) {
				if strings.HasPrefix(word, "#") {
					data.Form.AllTags += strings.TrimPrefix(word, "#") + " "
				} else if !strings.HasPrefix(word, "!") {
					data.Form.Search += word + " "
				}
			}
			data.Form.AllTags = strings.TrimSpace(data.Form.AllTags)
			data.Form.Search = strings.TrimSpace(data.Form.Search)
		}
	} else {
		part := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, root+"/"), "/edit")
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 || r.URL.Path != root+"/"+strconv.FormatInt(id, 10)+"/edit" {
			http.NotFound(w, r)
			return
		}
		data.Form, err = getBundle(r, cfg, db, user.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.PageTitle = "Edit bundle - Linkding"
		data.Heading = "Edit bundle"
		data.Action = r.URL.Path
	}
	status := 200
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			http.Error(w, "CSRF verification failed", 403)
			return
		}
		data.Form.Name = strings.TrimSpace(r.PostForm.Get("name"))
		data.Form.Search = strings.TrimSpace(r.PostForm.Get("search"))
		data.Form.AnyTags = strings.TrimSpace(r.PostForm.Get("any_tags"))
		data.Form.AllTags = strings.TrimSpace(r.PostForm.Get("all_tags"))
		data.Form.ExcludedTags = strings.TrimSpace(r.PostForm.Get("excluded_tags"))
		data.Form.FilterUnread = r.PostForm.Get("filter_unread")
		data.Form.FilterShared = r.PostForm.Get("filter_shared")
		data.NameError = validateBundleFormUI(data.Form)
		if data.NameError == "" {
			input := bundleInput{Name: &data.Form.Name, Search: &data.Form.Search, AnyTags: &data.Form.AnyTags, AllTags: &data.Form.AllTags, ExcludedTags: &data.Form.ExcludedTags, FilterUnread: &data.Form.FilterUnread, FilterShared: &data.Form.FilterShared}
			if data.Form.ID == 0 {
				_, err = createBundle(r, cfg, db, user.ID, input)
			} else {
				_, err = updateBundle(r, cfg, db, user.ID, data.Form, input)
			}
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			settingsFlash(w, cfg.URLPrefix(), "ld_bundle_success", "Bundle saved successfully.")
			http.Redirect(w, r, root, http.StatusFound)
			return
		}
		status = 422
	}
	data.Preview, err = renderBundlePreviewUI(r, cfg, db, repo, user.ID, data.Form)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	renderBundlesPage(w, r, data, status)
}

func validateBundleFormUI(bundle apiBundle) string {
	if bundle.Name == "" {
		return "This field is required."
	}
	for _, field := range []struct {
		value string
		limit int
	}{{bundle.Name, 256}, {bundle.Search, 256}, {bundle.AnyTags, 1024}, {bundle.AllTags, 1024}, {bundle.ExcludedTags, 1024}} {
		if len([]rune(field.value)) > field.limit {
			return "Ensure this value has at most " + strconv.Itoa(field.limit) + " characters."
		}
	}
	for _, filter := range []string{bundle.FilterUnread, bundle.FilterShared} {
		if filter != "" && filter != "off" && filter != "yes" && filter != "no" {
			return "Select a valid choice."
		}
	}
	return ""
}

func serveBundlesPreviewUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, repo *bookmarks.Repository, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			http.Error(w, "CSRF verification failed", 403)
			return
		}
	}
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		values = r.PostForm
	}
	bundle := apiBundle{Search: values.Get("search"), AnyTags: values.Get("any_tags"), AllTags: values.Get("all_tags"), ExcludedTags: values.Get("excluded_tags"), FilterUnread: values.Get("filter_unread"), FilterShared: values.Get("filter_shared")}
	fragment, err := renderBundlePreviewUI(r, cfg, db, repo, user.ID, bundle)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	integrationHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(fragment))
}

func renderBundlePreviewUI(r *http.Request, cfg config.Config, db *sql.DB, repo *bookmarks.Repository, ownerID int64, bundle apiBundle) (template.HTML, error) {
	profile, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, ownerID)
	if err != nil {
		return "", err
	}
	limit := positiveIntOr(profile.Get("items_per_page"), 30)
	page := positiveIntOr(r.URL.Query().Get("page"), 1)
	filter := bookmarks.PreviewBundle{Search: bundle.Search, AnyTags: bundle.AnyTags, AllTags: bundle.AllTags, ExcludedTags: bundle.ExcludedTags, FilterUnread: bundle.FilterUnread, FilterShared: bundle.FilterShared}
	items, count, err := repo.ListBundlePreview(r.Context(), ownerID, filter, bookmarks.ListOptions{Limit: limit, Offset: (page - 1) * limit})
	if err != nil {
		return "", err
	}
	pages := max(1, (int(count)+limit-1)/limit)
	if page > pages {
		page = pages
		items, _, err = repo.ListBundlePreview(r.Context(), ownerID, filter, bookmarks.ListOptions{Limit: limit, Offset: (page - 1) * limit})
		if err != nil {
			return "", err
		}
	}
	data := bundlePreviewData{Count: count, Page: page, Prefix: cfg.URLPrefix(), LinkTarget: profile.Get("bookmark_link_target"), DescriptionDisplay: profile.Get("bookmark_description_display"), DescriptionMaxLines: positiveIntOr(profile.Get("bookmark_description_max_lines"), 1), ShowURL: profile.Get("display_url") != "", ShowFavicons: profile.Get("enable_favicons") != "", ShowPreviews: profile.Get("enable_preview_images") != "", ShowNotes: profile.Get("permanent_notes") != "", StickyPagination: profile.Get("sticky_pagination") != "", HasPrevious: page > 1, HasNext: page < pages}
	if data.HasPrevious {
		data.PreviousURL = r.URL.Path + "?" + pageQuery(r.URL.RawQuery, page-1)
	}
	if data.HasNext {
		data.NextURL = r.URL.Path + "?" + pageQuery(r.URL.RawQuery, page+1)
	}
	for _, number := range visiblePageNumbers(page, pages) {
		if number == -1 {
			data.PageLinks = append(data.PageLinks, listPageLink{Ellipsis: true})
		} else {
			data.PageLinks = append(data.PageLinks, listPageLink{Number: number, URL: r.URL.Path + "?" + pageQuery(r.URL.RawQuery, number), Active: number == page})
		}
	}
	for _, item := range items {
		title := item.Title
		if title == "" {
			title = item.URL
		}
		row := bundlePreviewRow{ID: item.ID, URL: item.URL, Title: title, Description: item.Description, NotesHTML: markdown.Render(item.Notes), SnapshotURL: item.WebArchiveSnapshotURL}
		if row.SnapshotURL == "" {
			row.SnapshotURL = "https://web.archive.org/web/" + item.DateAdded.UTC().Format("20060102150405") + "/" + item.URL
		}
		switch profile.Get("bookmark_date_display") {
		case "hidden":
		case "absolute":
			row.Date = absoluteBookmarkDate(item.DateAdded, time.Now())
		default:
			row.Date = relativeBookmarkDate(item.DateAdded, time.Now())
		}
		if item.Unread {
			row.CSS = "unread"
		}
		if item.Shared {
			if row.CSS != "" {
				row.CSS += " "
			}
			row.CSS += "shared"
		}
		if item.FaviconFile != "" {
			row.FaviconURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.FaviconFile, "/")
		}
		if item.PreviewImageFile != "" {
			row.PreviewURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.PreviewImageFile, "/")
		}
		for _, name := range item.TagNames {
			row.Tags = append(row.Tags, listTag{Name: name, Query: addedTagQuery(r.URL.RawQuery, r.URL.Query().Get("q"), name, profile.Get("legacy_search") != "")})
		}
		data.Items = append(data.Items, row)
	}
	var output bytes.Buffer
	if err := bundlesPreviewTemplate.Execute(&output, data); err != nil {
		return "", err
	}
	return template.HTML(output.String()), nil
}
