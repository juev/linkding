package httpserver

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

//go:embed tags_page.html tags_modal.html
var tagsUIFiles embed.FS

var tagsPageTemplate = template.Must(template.ParseFS(tagsUIFiles, "tags_page.html"))
var tagsModalTemplate = template.Must(template.ParseFS(tagsUIFiles, "tags_modal.html"))

type tagUIRow struct {
	ID           int64
	Name         string
	Count        int64
	URL, EditURL string
}

type tagsPageData struct {
	Prefix, Theme, CustomCSSHash, CSRFToken, Search, Sort, PreviousURL, NextURL, Flash string
	CustomCSS, EnableSharing, IsSuperuser, UnusedOnly, HasPrevious, HasNext, Filtered  bool
	Total, FilterCount                                                                 int
	Rows                                                                               []tagUIRow
	PageLinks                                                                          []listPageLink
	ToastHTML                                                                          template.HTML
}

type tagModalData struct {
	Prefix, CSRFToken, Title, Action, CloseURL, Name, NameError, Target, TargetError, Merge, MergeError, Submit string
	MergeMode                                                                                                   bool
}

func serveTagsUI(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository) {
	root := cfg.URLPrefix() + "tags"
	path := r.URL.Path
	if path != root && path != root+"/new" && path != root+"/merge" && !(strings.HasPrefix(path, root+"/") && strings.HasSuffix(path, "/edit")) {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if path == root {
		serveTagsIndex(w, r, cfg, db, user)
		return
	}
	serveTagModal(w, r, cfg, db, user)
}

func serveTagsIndex(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
		id, err := strconv.ParseInt(r.PostForm.Get("delete_tag"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		var name string
		err = db.QueryRowContext(r.Context(), `SELECT name FROM bookmarks_tag WHERE id = `+assetMarker(cfg.DBEngine, 1)+` AND owner_id = `+assetMarker(cfg.DBEngine, 2), id, user.ID).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil || deleteTag(r, cfg, db, id, user.ID) != nil {
			http.Error(w, "Server error", 500)
			return
		}
		settingsFlash(w, cfg.URLPrefix(), "ld_tag_success", `Tag "`+name+`" deleted successfully.`)
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
		return
	}
	profile, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
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
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := tagsPageData{Prefix: cfg.URLPrefix(), Theme: profile.Get("theme"), CustomCSS: profile.Get("custom_css") != "", EnableSharing: profile.Get("enable_sharing") != "", IsSuperuser: user.IsSuperuser, CSRFToken: masked, Search: strings.TrimSpace(r.URL.Query().Get("search")), Sort: r.URL.Query().Get("sort"), UnusedOnly: r.URL.Query().Get("unused") == "true"}
	if data.Sort == "" {
		data.Sort = "name-asc"
	}
	if data.CustomCSS {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&data.CustomCSSHash)
	}
	data.Flash = takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_tag_success")
	query := `SELECT t.id,t.name,count(bt.bookmark_id) FROM bookmarks_tag t LEFT JOIN bookmarks_bookmark_tags bt ON bt.tag_id=t.id WHERE t.owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` GROUP BY t.id,t.name`
	rows, err := db.QueryContext(r.Context(), query, user.ID)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	all := []tagUIRow{}
	for rows.Next() {
		var item tagUIRow
		if err := rows.Scan(&item.ID, &item.Name, &item.Count); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		all = append(all, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Total = len(all)
	filtered := make([]tagUIRow, 0, len(all))
	for _, item := range all {
		if data.Search != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(data.Search)) || data.UnusedOnly && item.Count != 0 {
			continue
		}
		item.URL = cfg.URLPrefix() + "bookmarks?q=" + url.QueryEscape("#"+item.Name)
		item.EditURL = cfg.URLPrefix() + "tags/" + strconv.FormatInt(item.ID, 10) + "/edit?" + r.URL.RawQuery
		filtered = append(filtered, item)
	}
	data.FilterCount = len(filtered)
	data.Filtered = data.Search != "" || data.UnusedOnly
	slices.SortFunc(filtered, func(a, b tagUIRow) int {
		switch data.Sort {
		case "count-asc":
			if a.Count != b.Count {
				if a.Count < b.Count {
					return -1
				}
				return 1
			}
		case "count-desc":
			if a.Count != b.Count {
				if a.Count > b.Count {
					return -1
				}
				return 1
			}
		case "name-desc":
			return strings.Compare(strings.ToLower(b.Name), strings.ToLower(a.Name))
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	page := 1
	if parsed, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && parsed > 0 {
		page = parsed
	}
	pages := (len(filtered) + 49) / 50
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * 50
	end := min(start+50, len(filtered))
	data.Rows = filtered[start:end]
	if page > 1 {
		data.HasPrevious = true
		data.PreviousURL = cfg.URLPrefix() + "tags?" + pageQuery(r.URL.RawQuery, page-1)
	}
	if page < pages {
		data.HasNext = true
		data.NextURL = cfg.URLPrefix() + "tags?" + pageQuery(r.URL.RawQuery, page+1)
	}
	for _, number := range visiblePageNumbers(page, pages) {
		if number == -1 {
			data.PageLinks = append(data.PageLinks, listPageLink{Ellipsis: true})
		} else {
			data.PageLinks = append(data.PageLinks, listPageLink{Number: number, URL: cfg.URLPrefix() + "tags?" + pageQuery(r.URL.RawQuery, number), Active: number == page})
		}
	}
	data.ToastHTML, err = renderPageToasts(r.Context(), db, cfg, user.ID, data.CSRFToken, r.URL.Path)
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
	if err := tagsPageTemplate.Execute(w, data); err != nil {
		return
	}
}

func serveTagModal(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	root := cfg.URLPrefix() + "tags"
	data := tagModalData{Prefix: cfg.URLPrefix(), CloseURL: root, Action: r.URL.RequestURI(), Submit: "Save"}
	var tagID int64
	switch {
	case r.URL.Path == root+"/new":
		data.Title = "Create Tag"
	case r.URL.Path == root+"/merge":
		data.Title = "Merge Tags"
		data.Submit = "Merge Tags"
		data.MergeMode = true
	default:
		part := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, root+"/"), "/edit")
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 || r.URL.Path != root+"/"+strconv.FormatInt(id, 10)+"/edit" {
			http.NotFound(w, r)
			return
		}
		tagID = id
		data.Title = "Edit Tag"
		if err := db.QueryRowContext(r.Context(), `SELECT name FROM bookmarks_tag WHERE id = `+assetMarker(cfg.DBEngine, 1)+` AND owner_id = `+assetMarker(cfg.DBEngine, 2), id, user.ID).Scan(&data.Name); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if r.URL.RawQuery != "" {
			data.CloseURL += "?" + r.URL.RawQuery
		}
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		var err error
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	var err error
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
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
		if data.MergeMode {
			data.Target = r.PostForm.Get("target_tag")
			data.Merge = r.PostForm.Get("merge_tags")
			data.TargetError, data.MergeError, err = mergeTagsUI(r, cfg, db, user.ID, data.Target, data.Merge)
		} else {
			data.Name = r.PostForm.Get("name")
			data.NameError, err = saveTagUI(r, cfg, db, user.ID, tagID, data.Name)
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if data.NameError == "" && data.TargetError == "" && data.MergeError == "" {
			message := ""
			if data.MergeMode {
				message = "Tags merged successfully."
			} else if tagID == 0 {
				message = `Tag "` + strings.TrimSpace(data.Name) + `" created successfully.`
			} else {
				message = `Tag "` + strings.TrimSpace(data.Name) + `" updated successfully.`
			}
			settingsFlash(w, cfg.URLPrefix(), "ld_tag_success", message)
			http.Redirect(w, r, root, http.StatusFound)
			return
		}
	}
	var output bytes.Buffer
	if err := tagsModalTemplate.Execute(&output, data); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	integrationHeaders(w)
	if r.Method == http.MethodPost {
		w.Header().Set("Content-Type", "text/vnd.turbo-stream.html; charset=utf-8")
		_, _ = w.Write([]byte(`<turbo-stream action="replace" method="morph" target="tag-modal"><template>`))
		_, _ = w.Write(output.Bytes())
		_, _ = w.Write([]byte(`</template></turbo-stream>`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(output.Bytes())
}

func saveTagUI(r *http.Request, cfg config.Config, db *sql.DB, ownerID, tagID int64, name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "-")
	if name == "" {
		return "This field is required.", nil
	}
	if len([]rune(name)) > 64 {
		return "Ensure this value has at most 64 characters (it has " + strconv.Itoa(len([]rune(name))) + ").", nil
	}
	query := `SELECT id FROM bookmarks_tag WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` AND LOWER(name) = LOWER(` + assetMarker(cfg.DBEngine, 2) + `) LIMIT 1`
	var existing int64
	err := db.QueryRowContext(r.Context(), query, ownerID, name).Scan(&existing)
	if err == nil && existing != tagID {
		return `Tag "` + name + `" already exists.`, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if tagID == 0 {
		query = `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (` + assetMarker(cfg.DBEngine, 1) + `,` + assetMarker(cfg.DBEngine, 2) + `,` + assetMarker(cfg.DBEngine, 3) + `)`
		_, err = db.ExecContext(r.Context(), query, name, time.Now().UTC(), ownerID)
	} else {
		query = `UPDATE bookmarks_tag SET name = ` + assetMarker(cfg.DBEngine, 1) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 2) + ` AND owner_id = ` + assetMarker(cfg.DBEngine, 3)
		_, err = db.ExecContext(r.Context(), query, name, tagID, ownerID)
	}
	return "", err
}

func mergeTagsUI(r *http.Request, cfg config.Config, db *sql.DB, ownerID int64, target, merge string) (string, string, error) {
	targetNames := strings.Fields(target)
	if len(targetNames) != 1 {
		return "Please enter only one tag name for the target tag.", "", nil
	}
	targetID, err := tagIDByNameUI(r, cfg, db, ownerID, targetNames[0])
	if errors.Is(err, sql.ErrNoRows) {
		return `Tag "` + targetNames[0] + `" does not exist.`, "", nil
	}
	if err != nil {
		return "", "", err
	}
	mergeNames := strings.Fields(merge)
	if len(mergeNames) == 0 {
		return "", "Please enter at least one tag to merge.", nil
	}
	ids := []int64{}
	for _, name := range mergeNames {
		id, err := tagIDByNameUI(r, cfg, db, ownerID, name)
		if errors.Is(err, sql.ErrNoRows) {
			return "", `Tag "` + name + `" does not exist.`, nil
		}
		if err != nil {
			return "", "", err
		}
		if id == targetID {
			return "", "The target tag cannot be selected for merging.", nil
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	for _, id := range ids {
		query := `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) SELECT bt.bookmark_id,` + assetMarker(cfg.DBEngine, 1) + ` FROM bookmarks_bookmark_tags bt WHERE bt.tag_id = ` + assetMarker(cfg.DBEngine, 2) + ` AND NOT EXISTS (SELECT 1 FROM bookmarks_bookmark_tags old WHERE old.bookmark_id=bt.bookmark_id AND old.tag_id = ` + assetMarker(cfg.DBEngine, 3) + `)`
		if _, err := tx.ExecContext(r.Context(), query, targetID, id, targetID); err != nil {
			return "", "", err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmark_tags WHERE tag_id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
			return "", "", err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_tag WHERE id = `+assetMarker(cfg.DBEngine, 1)+` AND owner_id = `+assetMarker(cfg.DBEngine, 2), id, ownerID); err != nil {
			return "", "", err
		}
	}
	return "", "", tx.Commit()
}

func tagIDByNameUI(r *http.Request, cfg config.Config, db *sql.DB, ownerID int64, name string) (int64, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "-")
	var id int64
	err := db.QueryRowContext(r.Context(), `SELECT id FROM bookmarks_tag WHERE owner_id = `+assetMarker(cfg.DBEngine, 1)+` AND LOWER(name) = LOWER(`+assetMarker(cfg.DBEngine, 2)+`) LIMIT 1`, ownerID, name).Scan(&id)
	return id, err
}
