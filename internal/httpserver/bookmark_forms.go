package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

//go:embed bookmark_form.html
var bookmarkFormTemplateFile embed.FS
var bookmarkFormTemplate = template.Must(template.ParseFS(bookmarkFormTemplateFile, "bookmark_form.html"))

type bookmarkFormPage struct {
	Prefix, CSRFToken, Theme, CustomCSSHash, PageTitle, Heading, Action, ReturnURL                  string
	URL, Title, Description, Notes, Tags, URLError                                                  string
	CustomCSS, EnableSharing, EnablePublicSharing, IsSuperuser, Unread, Shared, AutoClose, HasNotes bool
	BookmarkID                                                                                      int64
	Status                                                                                          int
	ToastHTML                                                                                       template.HTML
}

func serveBookmarkForm(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository) {
	path := r.URL.Path
	if path != root+"new" && !strings.HasSuffix(path, "/edit") {
		writeNotFound(w, r)
		return
	}
	if path != root+"new" {
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, root), "/edit"), 10, 64)
		if err != nil || id <= 0 || path != root+strconv.FormatInt(id, 10)+"/edit" {
			writeNotFound(w, r)
			return
		}
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	form, err := settings.LoadProfileForm(r.Context(), db, cfg.DBEngine, user.ID)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := bookmarkFormPage{Prefix: cfg.URLPrefix(), Theme: form.Get("theme"), CustomCSS: form.Get("custom_css") != "", EnableSharing: form.Get("enable_sharing") != "", EnablePublicSharing: form.Get("enable_public_sharing") != "", IsSuperuser: user.IsSuperuser, ReturnURL: cfg.URLPrefix() + "bookmarks", Status: 200}
	if data.CustomCSS {
		_ = db.QueryRowContext(r.Context(), `SELECT custom_css_hash FROM bookmarks_userprofile WHERE user_id = `+assetMarker(cfg.DBEngine, 1), user.ID).Scan(&data.CustomCSSHash)
	}
	var original bookmarks.Bookmark
	if path == root+"new" {
		data.PageTitle = "New bookmark - Linkding"
		data.Heading = "New bookmark"
		data.Action = root + "new"
		data.URL = r.URL.Query().Get("url")
		data.Title = r.URL.Query().Get("title")
		data.Description = r.URL.Query().Get("description")
		data.Notes = r.URL.Query().Get("notes")
		data.Tags = r.URL.Query().Get("tags")
		data.AutoClose = r.URL.Query().Has("auto_close")
		data.Unread = form.Get("default_mark_unread") != ""
		data.Shared = form.Get("default_mark_shared") != ""
	} else {
		idString := strings.TrimSuffix(strings.TrimPrefix(path, root), "/edit")
		id, err := strconv.ParseInt(idString, 10, 64)
		if err != nil || id <= 0 || path != root+strconv.FormatInt(id, 10)+"/edit" {
			writeNotFound(w, r)
			return
		}
		original, err = repo.GetByID(r.Context(), user.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.BookmarkID = id
		data.PageTitle = "Edit bookmark - Linkding"
		data.Heading = "Edit bookmark"
		data.ReturnURL = safeBookmarkReturnURL(r.URL.Query().Get("return_url"), cfg.URLPrefix()+"bookmarks")
		data.Action = root + idString + "/edit?return_url=" + url.QueryEscape(data.ReturnURL)
		data.URL = original.URL
		data.Title = original.Title
		data.Description = original.Description
		data.Notes = original.Notes
		data.Tags = strings.Join(original.TagNames, " ")
		data.Unread = original.Unread
		data.Shared = original.Shared
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
		data.URL = r.PostForm.Get("url")
		data.Title = r.PostForm.Get("title")
		data.Description = r.PostForm.Get("description")
		data.Notes = r.PostForm.Get("notes")
		data.Tags = r.PostForm.Get("tag_string")
		data.Unread = r.PostForm.Get("unread") != ""
		data.Shared = r.PostForm.Get("shared") != ""
		data.AutoClose = r.PostForm.Get("auto_close") == "True"
		if data.URL == "" {
			data.URLError = "This field is required."
		} else if len([]rune(data.URL)) > 2048 {
			data.URLError = "Ensure this value has at most 2048 characters."
		} else if !cfg.DisableURLValidation && !validBookmarkURL(data.URL) {
			data.URLError = "Enter a valid URL."
		}
		if len([]rune(data.Title)) > 512 {
			data.URLError = "Title is too long."
		}
		if data.URLError == "" {
			tags := bookmarks.ParseTagString(strings.ReplaceAll(data.Tags, " ", ","), ",")
			if data.BookmarkID == 0 {
				_, _, err = repo.CreateOrUpdateData(r.Context(), user.ID, bookmarks.CreateInput{URL: data.URL, Title: data.Title, Description: data.Description, Notes: data.Notes, Unread: data.Unread, Shared: data.Shared, TagNames: tags})
			} else {
				_, err = repo.UpdateData(r.Context(), user.ID, data.BookmarkID, bookmarks.UpdateInput{URL: &data.URL, Title: &data.Title, Description: &data.Description, Notes: &data.Notes, Unread: &data.Unread, Shared: &data.Shared, TagNames: &tags})
			}
			if errors.Is(err, bookmarks.ErrDuplicateURL) {
				data.URLError = "A bookmark with this URL already exists."
			} else if err != nil {
				http.Error(w, "Server error", 500)
				return
			} else {
				destination := data.ReturnURL
				if data.BookmarkID == 0 && data.AutoClose {
					destination = cfg.URLPrefix() + "bookmarks/close"
				}
				writeRedirect(w, r, destination)
				return
			}
		}
		data.Status = 422
	}
	data.HasNotes = data.Notes != ""
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
	integrationHeaders(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate, private")
	w.WriteHeader(data.Status)
	_ = bookmarkFormTemplate.Execute(w, data)
}

var safeBookmarkReturnPattern = regexp.MustCompile(`^/[a-z]+`)

func safeBookmarkReturnURL(candidate, fallback string) string {
	if candidate == "" || !safeBookmarkReturnPattern.MatchString(candidate) {
		return fallback
	}
	return candidate
}
