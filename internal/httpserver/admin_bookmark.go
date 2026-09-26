package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_bookmark.html admin_sidebar.html
var adminBookmarkFile embed.FS
var adminBookmarkTemplate = adminSidebarTemplate(adminBookmarkFile, "admin_bookmark.html")

type adminBookmarkOption struct {
	ID       int64
	Label    string
	Selected bool
}

type adminBookmarkData struct {
	Language                                            string
	DashboardApps                                       []adminDashboardApp
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	URL, URLNormalized, BookmarkTitle, Description      string
	ObjectName                                          string
	Notes, WebsiteTitle, WebsiteDescription             string
	WebArchiveURL, FaviconFile, PreviewImageFile        string
	DateAddedDate, DateAddedTime                        string
	DateModifiedDate, DateModifiedTime                  string
	DateAccessedDate, DateAccessedTime                  string
	Error                                               string
	ID, OwnerID, LatestSnapshotID                       int64
	Unread, Archived, Shared                            bool
	ConfirmDelete, CanChange, CanDelete                 bool
	History                                             bool
	HistoryRows                                         []adminTagHistoryRow
	Owners                                              []adminOwnerOption
	Tags, Snapshots                                     []adminBookmarkOption
	SelectedTagIDs                                      map[int64]bool
	Deletion                                            adminSingleDeletion
}

func serveAdminBookmark(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/bookmark/"
	part := strings.TrimPrefix(r.URL.Path, base)
	var action string
	var id int64
	if part == "add/" {
		action = "add"
	} else {
		pieces := strings.Split(part, "/")
		if len(pieces) != 3 || pieces[2] != "" || (pieces[1] != "change" && pieces[1] != "delete" && pieces[1] != "history") {
			writeNotFound(w, r)
			return
		}
		parsed, err := strconv.ParseInt(pieces[0], 10, 64)
		if err != nil || parsed <= 0 {
			writeNotFound(w, r)
			return
		}
		id, action = parsed, pieces[1]
	}
	if (action == "add" && !permissions.Add) || ((action == "change" || action == "history") && !permissions.Change && !permissions.View) || (action == "delete" && !permissions.Delete) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if action == "history" && r.Method == http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	location, err := adminTagLocation(cfg.TimeZone)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := adminBookmarkData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add bookmark", ConfirmDelete: action == "delete", History: action == "history", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete, SelectedTagIDs: make(map[int64]bool)}
	if action == "change" {
		data.Title = "Change bookmark"
		if !permissions.Change {
			data.Title = "View bookmark"
		}
	} else if action == "delete" {
		data.Title = "Delete bookmark"
	}
	if id != 0 {
		var websiteTitle, websiteDescription sql.NullString
		var accessed sql.NullTime
		var snapshot sql.NullInt64
		var added, modified time.Time
		err := db.QueryRowContext(r.Context(), `SELECT url,url_normalized,title,description,notes,website_title,website_description,web_archive_snapshot_url,favicon_file,preview_image_file,unread,is_archived,shared,date_added,date_modified,date_accessed,owner_id,latest_snapshot_id FROM bookmarks_bookmark WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.URL, &data.URLNormalized, &data.BookmarkTitle, &data.Description, &data.Notes, &websiteTitle, &websiteDescription, &data.WebArchiveURL, &data.FaviconFile, &data.PreviewImageFile, &data.Unread, &data.Archived, &data.Shared, &added, &modified, &accessed, &data.OwnerID, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.WebsiteTitle, data.WebsiteDescription = websiteTitle.String, websiteDescription.String
		data.ObjectName = adminBookmarkRepr(data.BookmarkTitle, data.URL)
		data.LatestSnapshotID = snapshot.Int64
		data.DateAddedDate, data.DateAddedTime = adminBookmarkDateParts(added, location)
		data.DateModifiedDate, data.DateModifiedTime = adminBookmarkDateParts(modified, location)
		if accessed.Valid {
			data.DateAccessedDate, data.DateAccessedTime = adminBookmarkDateParts(accessed.Time, location)
		}
		rows, err := db.QueryContext(r.Context(), `SELECT tag_id FROM bookmarks_bookmark_tags WHERE bookmark_id = `+assetMarker(cfg.DBEngine, 1), id)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		for rows.Next() {
			var tagID int64
			if err := rows.Scan(&tagID); err != nil {
				rows.Close()
				http.Error(w, "Server error", 500)
				return
			}
			data.SelectedTagIDs[tagID] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	if action == "history" {
		data.Title = "Change history: " + data.ObjectName
		rows, err := db.QueryContext(r.Context(), `SELECT l.action_time,u.username,l.action_flag,l.change_message FROM django_admin_log AS l JOIN django_content_type AS c ON c.id=l.content_type_id JOIN auth_user AS u ON u.id=l.user_id WHERE c.app_label = `+assetMarker(cfg.DBEngine, 1)+` AND c.model = `+assetMarker(cfg.DBEngine, 2)+` AND l.object_id = `+assetMarker(cfg.DBEngine, 3)+` ORDER BY l.action_time DESC,l.id DESC LIMIT 100`, "bookmarks", "bookmark", strconv.FormatInt(id, 10))
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		for rows.Next() {
			var happened time.Time
			var entry adminTagHistoryRow
			var flag int
			var message string
			if err := rows.Scan(&happened, &entry.Username, &flag, &message); err != nil {
				rows.Close()
				http.Error(w, "Server error", 500)
				return
			}
			entry.Date = adminHistoryDate(happened.In(location))
			entry.Action = adminTagHistoryMessage(flag, message)
			data.HistoryRows = append(data.HistoryRows, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		rows.Close()
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			writeCSRFFailure(w, r)
			return
		}
		if action == "delete" {
			if r.PostForm.Get("post") != "yes" {
				http.Error(w, "Invalid form", 400)
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "bookmark", strconv.FormatInt(id, 10), adminBookmarkRepr(data.BookmarkTitle, data.URL), 3, ""); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			repo := bookmarks.NewRepository(db, cfg.DBEngine)
			files, err := repo.DeleteDataTx(r.Context(), tx, data.OwnerID, id)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			removeStoredFile(filepath.Join(cfg.DataDir, "previews"), files.Preview)
			for _, name := range files.Assets {
				removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
			}
			writeRedirect(w, r, base)
			return
		}
		if action == "change" && !permissions.Change {
			http.Error(w, "Forbidden", 403)
			return
		}
		previous := data
		data.readPost(r)
		added, modified, accessed, validationErr := data.validate(r, cfg, db, location)
		if validationErr != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if data.Error == "" {
			if err := saveAdminBookmark(r, cfg, db, user.ID, &data, &previous, action, added, modified, accessed); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			redirect := base
			if _, ok := r.PostForm["_addanother"]; ok {
				redirect = base + "add/"
			} else if _, ok := r.PostForm["_continue"]; ok {
				redirect = base + strconv.FormatInt(data.ID, 10) + "/change/"
			}
			writeRedirect(w, r, redirect)
			return
		}
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
	if err := data.loadOptions(r, db); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	models, err := loadAdminModels(r, db, cfg, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	if data.ConfirmDelete {
		data.Deletion, err = adminSingleDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), "bookmark", strconv.FormatInt(id, 10), data.ObjectName)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		localizeAdminDeletionGraph(data.Language, data.Deletion.Summary, data.Deletion.Nodes)
	}
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminBookmarkTemplate.Execute(w, data)
	}
}

func adminBookmarkDateParts(value time.Time, location *time.Location) (string, string) {
	local := value.In(location)
	return local.Format("2006-01-02"), local.Format("15:04:05")
}

func (data *adminBookmarkData) readPost(r *http.Request) {
	data.URL = strings.TrimSpace(r.PostForm.Get("url"))
	data.URLNormalized = strings.TrimSpace(r.PostForm.Get("url_normalized"))
	data.BookmarkTitle = strings.TrimSpace(r.PostForm.Get("title"))
	data.Description = strings.TrimSpace(r.PostForm.Get("description"))
	data.Notes = strings.TrimSpace(r.PostForm.Get("notes"))
	data.WebsiteTitle = strings.TrimSpace(r.PostForm.Get("website_title"))
	data.WebsiteDescription = strings.TrimSpace(r.PostForm.Get("website_description"))
	data.WebArchiveURL = strings.TrimSpace(r.PostForm.Get("web_archive_snapshot_url"))
	data.FaviconFile = strings.TrimSpace(r.PostForm.Get("favicon_file"))
	data.PreviewImageFile = strings.TrimSpace(r.PostForm.Get("preview_image_file"))
	data.Unread = r.PostForm.Has("unread")
	data.Archived = r.PostForm.Has("is_archived")
	data.Shared = r.PostForm.Has("shared")
	data.DateAddedDate, data.DateAddedTime = r.PostForm.Get("date_added_0"), r.PostForm.Get("date_added_1")
	data.DateModifiedDate, data.DateModifiedTime = r.PostForm.Get("date_modified_0"), r.PostForm.Get("date_modified_1")
	data.DateAccessedDate, data.DateAccessedTime = r.PostForm.Get("date_accessed_0"), r.PostForm.Get("date_accessed_1")
	data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("owner"), 10, 64)
	if raw := r.PostForm.Get("latest_snapshot"); raw != "" {
		var err error
		data.LatestSnapshotID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || data.LatestSnapshotID <= 0 {
			data.Error = "Select a valid latest snapshot."
		}
	} else {
		data.LatestSnapshotID = 0
	}
	data.SelectedTagIDs = make(map[int64]bool)
	for _, raw := range r.PostForm["tags"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			data.Error = "Select valid tags."
			continue
		}
		data.SelectedTagIDs[id] = true
	}
}

func (data *adminBookmarkData) validate(r *http.Request, cfg config.Config, db *sql.DB, location *time.Location) (time.Time, time.Time, sql.NullTime, error) {
	var added, modified time.Time
	var accessed sql.NullTime
	switch {
	case data.Error != "":
	case data.URL == "" || len([]rune(data.URL)) > 2048 || (!cfg.DisableURLValidation && !validBookmarkURL(data.URL)):
		data.Error = "Enter a valid URL of at most 2048 characters."
	case len([]rune(data.BookmarkTitle)) > 512 || len([]rune(data.WebsiteTitle)) > 512 || len([]rune(data.WebArchiveURL)) > 2048 || len([]rune(data.FaviconFile)) > 512 || len([]rune(data.PreviewImageFile)) > 512:
		data.Error = "One or more fields exceed their maximum length."
	case data.OwnerID <= 0:
		data.Error = "Select a valid owner."
	case len(data.SelectedTagIDs) == 0:
		data.Error = "Select at least one tag."
	}
	if data.Error == "" {
		var err error
		added, err = parseAdminTagDateTime(data.DateAddedDate, data.DateAddedTime, location)
		if err == nil {
			modified, err = parseAdminTagDateTime(data.DateModifiedDate, data.DateModifiedTime, location)
		}
		if err != nil {
			data.Error = "Enter valid dates and times."
		}
	}
	if data.Error == "" && (data.DateAccessedDate != "" || data.DateAccessedTime != "") {
		value, err := parseAdminTagDateTime(data.DateAccessedDate, data.DateAccessedTime, location)
		if err != nil {
			data.Error = "Enter a valid date accessed."
		} else {
			accessed = sql.NullTime{Time: value.UTC(), Valid: true}
		}
	}
	if data.Error != "" {
		return added, modified, accessed, nil
	}
	var exists bool
	if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.OwnerID).Scan(&exists); err != nil {
		return added, modified, accessed, err
	}
	if !exists {
		data.Error = "Select a valid owner."
		return added, modified, accessed, nil
	}
	if data.LatestSnapshotID > 0 {
		if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM bookmarks_bookmarkasset WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.LatestSnapshotID).Scan(&exists); err != nil {
			return added, modified, accessed, err
		}
		if !exists {
			data.Error = "Select a valid latest snapshot."
			return added, modified, accessed, nil
		}
	}
	for tagID := range data.SelectedTagIDs {
		if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM bookmarks_tag WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, tagID).Scan(&exists); err != nil {
			return added, modified, accessed, err
		}
		if !exists {
			data.Error = "Select valid tags."
			return added, modified, accessed, nil
		}
	}
	return added.UTC(), modified.UTC(), accessed, nil
}

func saveAdminBookmark(r *http.Request, cfg config.Config, db *sql.DB, actorID int64, data, previous *adminBookmarkData, action string, added, modified time.Time, accessed sql.NullTime) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var websiteTitle, websiteDescription, latestSnapshot any
	if data.WebsiteTitle != "" {
		websiteTitle = data.WebsiteTitle
	}
	if data.WebsiteDescription != "" {
		websiteDescription = data.WebsiteDescription
	}
	if data.LatestSnapshotID > 0 {
		latestSnapshot = data.LatestSnapshotID
	}
	values := []any{data.URL, bookmarks.NormalizeURL(data.URL), data.BookmarkTitle, data.Description, data.Notes, websiteTitle, websiteDescription, data.WebArchiveURL, data.FaviconFile, data.PreviewImageFile, data.Unread, data.Archived, data.Shared, added, modified, accessed, data.OwnerID, latestSnapshot}
	columns := []string{"url", "url_normalized", "title", "description", "notes", "website_title", "website_description", "web_archive_snapshot_url", "favicon_file", "preview_image_file", "unread", "is_archived", "shared", "date_added", "date_modified", "date_accessed", "owner_id", "latest_snapshot_id"}
	if action == "add" {
		query := `INSERT INTO bookmarks_bookmark(` + strings.Join(columns, ",") + `) VALUES (` + adminBundleMarkers(cfg.DBEngine, len(values)) + `) RETURNING id`
		if err := tx.QueryRowContext(r.Context(), query, values...).Scan(&data.ID); err != nil {
			return err
		}
	} else {
		assignments := make([]string, len(columns))
		for i, column := range columns {
			assignments[i] = column + ` = ` + assetMarker(cfg.DBEngine, i+1)
		}
		values = append(values, data.ID)
		query := `UPDATE bookmarks_bookmark SET ` + strings.Join(assignments, ",") + ` WHERE id = ` + assetMarker(cfg.DBEngine, len(values))
		if _, err := tx.ExecContext(r.Context(), query, values...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id = `+assetMarker(cfg.DBEngine, 1), data.ID); err != nil {
			return err
		}
	}
	for tagID := range data.SelectedTagIDs {
		query := `INSERT INTO bookmarks_bookmark_tags(bookmark_id,tag_id) VALUES (` + assetMarker(cfg.DBEngine, 1) + `,` + assetMarker(cfg.DBEngine, 2) + `)`
		if _, err := tx.ExecContext(r.Context(), query, data.ID, tagID); err != nil {
			return err
		}
	}
	flag, message := 1, adminAdditionMessage
	if action == "change" {
		flag, message = 2, adminChangeMessage(adminBookmarkChangedFields(*previous, *data))
	}
	if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actorID, "bookmarks", "bookmark", strconv.FormatInt(data.ID, 10), adminBookmarkRepr(data.BookmarkTitle, data.URL), flag, message); err != nil {
		return err
	}
	return tx.Commit()
}

func (data *adminBookmarkData) loadOptions(r *http.Request, db *sql.DB) error {
	owners, err := db.QueryContext(r.Context(), `SELECT id,username FROM auth_user ORDER BY username`)
	if err != nil {
		return err
	}
	for owners.Next() {
		var option adminOwnerOption
		if err := owners.Scan(&option.ID, &option.Username); err != nil {
			owners.Close()
			return err
		}
		option.Selected = option.ID == data.OwnerID
		data.Owners = append(data.Owners, option)
	}
	err = owners.Err()
	owners.Close()
	if err != nil {
		return err
	}
	tags, err := db.QueryContext(r.Context(), `SELECT id,name FROM bookmarks_tag ORDER BY date_added DESC,id DESC`)
	if err != nil {
		return err
	}
	for tags.Next() {
		var option adminBookmarkOption
		if err := tags.Scan(&option.ID, &option.Label); err != nil {
			tags.Close()
			return err
		}
		option.Selected = data.SelectedTagIDs[option.ID]
		data.Tags = append(data.Tags, option)
	}
	err = tags.Err()
	tags.Close()
	if err != nil {
		return err
	}
	snapshots, err := db.QueryContext(r.Context(), `SELECT a.id,COALESCE(NULLIF(a.display_name,''),'Bookmark Asset #' || a.id) FROM bookmarks_bookmarkasset AS a ORDER BY a.id`)
	if err != nil {
		return err
	}
	for snapshots.Next() {
		var option adminBookmarkOption
		if err := snapshots.Scan(&option.ID, &option.Label); err != nil {
			snapshots.Close()
			return err
		}
		option.Selected = option.ID == data.LatestSnapshotID
		data.Snapshots = append(data.Snapshots, option)
	}
	err = snapshots.Err()
	snapshots.Close()
	return err
}
