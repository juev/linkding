package httpserver

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/markdown"
)

//go:embed bookmark_details.html
var bookmarkDetailsTemplateFile embed.FS
var bookmarkDetailsTemplate = template.Must(template.ParseFS(bookmarkDetailsTemplateFile, "bookmark_details.html"))

type bookmarkDetailsAsset struct {
	ID                        int64
	Name, Status, Size, Color string
	HasFile                   bool
}
type bookmarkDetailsData struct {
	Prefix, CSRFToken, Title, URL, Description, DateAdded, WebArchiveURL, FaviconURL, PreviewURL, LinkTarget, ActionURL, DeleteURL, CloseURL, EditURL string
	NotesHTML                                                                                                                                         template.HTML
	ID, LatestSnapshotID                                                                                                                              int64
	Tags                                                                                                                                              []listTag
	Assets                                                                                                                                            []bookmarkDetailsAsset
	Editable, Archived, Unread, Shared, EnableSharing, ShowLinkIcons, ShowPreview, SnapshotsEnabled, UploadsEnabled, PendingAssets                    bool
}

func renderBookmarkDetails(r *http.Request, cfg config.Config, db *sql.DB, repo *bookmarks.Repository, user auth.User, profile url.Values, csrfToken string) (template.HTML, error) {
	fragment := bookmarkDetailsData{Prefix: cfg.URLPrefix(), CSRFToken: csrfToken}
	id, err := strconv.ParseInt(r.URL.Query().Get("details"), 10, 64)
	if err != nil || id <= 0 {
		return executeBookmarkDetails(fragment)
	}
	query := `SELECT b.owner_id,b.shared,p.enable_sharing,p.enable_public_sharing,b.website_title,b.website_description,b.latest_snapshot_id
		FROM bookmarks_bookmark b JOIN bookmarks_userprofile p ON p.user_id=b.owner_id WHERE b.id = ` + assetMarker(cfg.DBEngine, 1)
	var ownerID int64
	var shared, sharing, publicSharing bool
	var websiteTitle, websiteDescription sql.NullString
	var latest sql.NullInt64
	err = db.QueryRowContext(r.Context(), query, id).Scan(&ownerID, &shared, &sharing, &publicSharing, &websiteTitle, &websiteDescription, &latest)
	if errors.Is(err, sql.ErrNoRows) {
		return executeBookmarkDetails(fragment)
	}
	if err != nil {
		return "", err
	}
	allowed := user.ID != 0 && ownerID == user.ID || shared && (user.ID != 0 && sharing || publicSharing)
	if !allowed {
		return executeBookmarkDetails(fragment)
	}
	item, err := repo.GetByID(r.Context(), ownerID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return executeBookmarkDetails(fragment)
	}
	if err != nil {
		return "", err
	}
	fragment.ID = id
	fragment.Title = item.Title
	fragment.URL = item.URL
	fragment.Description = item.Description
	fragment.DateAdded = formatBookmarkDetailsDate(item.DateAdded, r, cfg.TimeZone)
	fragment.NotesHTML = markdown.Render(item.Notes)
	fragment.Archived = item.IsArchived
	fragment.Unread = item.Unread
	fragment.Shared = item.Shared
	fragment.Editable = ownerID == user.ID && user.ID != 0
	fragment.EnableSharing = profile.Get("enable_sharing") != ""
	fragment.LinkTarget = profile.Get("bookmark_link_target")
	fragment.SnapshotsEnabled = cfg.EnableSnapshots
	fragment.UploadsEnabled = !cfg.DisableAssetUpload
	if fragment.Title == "" {
		fragment.Title = websiteTitle.String
	}
	if fragment.Title == "" {
		fragment.Title = item.URL
	}
	if fragment.Description == "" {
		fragment.Description = websiteDescription.String
	}
	if latest.Valid {
		fragment.LatestSnapshotID = latest.Int64
	}
	if item.FaviconFile != "" && profile.Get("enable_favicons") != "" {
		fragment.ShowLinkIcons = true
		fragment.FaviconURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.FaviconFile, "/")
	}
	if item.PreviewImageFile != "" && profile.Get("enable_preview_images") != "" {
		fragment.ShowPreview = true
		fragment.PreviewURL = cfg.URLPrefix() + "static/" + strings.TrimPrefix(item.PreviewImageFile, "/")
	}
	fragment.WebArchiveURL = item.WebArchiveSnapshotURL
	if fragment.WebArchiveURL == "" {
		fragment.WebArchiveURL = "https://web.archive.org/web/" + item.DateAdded.UTC().Format("20060102150405") + "/" + item.URL
	}
	values := r.URL.Query()
	closeQuery := orderedListQuery(r.URL.RawQuery, "", "", "details")
	detailsQuery := orderedListQuery(r.URL.RawQuery, "details", strconv.FormatInt(id, 10))
	base := r.URL.Path
	fragment.CloseURL = base
	if closeQuery != "" {
		fragment.CloseURL += "?" + closeQuery
	}
	fragment.ActionURL = base + "/action?" + detailsQuery
	fragment.DeleteURL = base + "/action"
	if closeQuery != "" {
		fragment.DeleteURL += "?" + closeQuery
	}
	fragment.EditURL = cfg.URLPrefix() + "bookmarks/" + strconv.FormatInt(id, 10) + "/edit?return_url=" + djangoURLQuote(base+"?"+detailsQuery)
	for _, name := range item.TagNames {
		fragment.Tags = append(fragment.Tags, listTag{Name: name, Query: addedTagQuery(r.URL.RawQuery, values.Get("q"), name, profile.Get("legacy_search") != "")})
	}
	query = `SELECT id,bookmark_id,date_created,file_size,asset_type,content_type,display_name,status,file,gzip FROM bookmarks_bookmarkasset WHERE bookmark_id = ` + assetMarker(cfg.DBEngine, 1) + ` ORDER BY id`
	rows, err := db.QueryContext(r.Context(), query, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return "", err
		}
		entry := bookmarkDetailsAsset{ID: asset.ID, Name: asset.DisplayName, Status: asset.Status, HasFile: asset.file != ""}
		if asset.FileSize != nil {
			entry.Size = humanFileSize(*asset.FileSize)
		}
		switch asset.Status {
		case "pending":
			entry.Color = "text-tertiary"
			fragment.PendingAssets = true
		case "failure":
			entry.Color = "text-error"
		default:
			entry.Color = "icon-color"
		}
		fragment.Assets = append(fragment.Assets, entry)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return executeBookmarkDetails(fragment)
}

func executeBookmarkDetails(data bookmarkDetailsData) (template.HTML, error) {
	var buffer bytes.Buffer
	if err := bookmarkDetailsTemplate.Execute(&buffer, data); err != nil {
		return "", err
	}
	return template.HTML(buffer.String()), nil
}
func cloneQuery(values url.Values) url.Values {
	result := url.Values{}
	for key, list := range values {
		result[key] = append([]string(nil), list...)
	}
	return result
}
func humanFileSize(size int64) string {
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " bytes"
	}
	if size < 1024*1024 {
		return strconv.FormatInt(size/1024, 10) + " KB"
	}
	return strconv.FormatInt(size/(1024*1024), 10) + " MB"
}
