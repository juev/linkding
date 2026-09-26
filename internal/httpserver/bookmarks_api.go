package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/metadata"
)

type apiBookmark struct {
	ID                    int64    `json:"id"`
	URL                   string   `json:"url"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	Notes                 string   `json:"notes"`
	WebArchiveSnapshotURL *string  `json:"web_archive_snapshot_url"`
	FaviconURL            *string  `json:"favicon_url"`
	PreviewImageURL       *string  `json:"preview_image_url"`
	IsArchived            bool     `json:"is_archived"`
	Unread                bool     `json:"unread"`
	Shared                bool     `json:"shared"`
	TagNames              []string `json:"tag_names"`
	DateAdded             string   `json:"date_added"`
	DateModified          string   `json:"date_modified"`
	WebsiteTitle          any      `json:"website_title"`
	WebsiteDescription    any      `json:"website_description"`
}

func serializeBookmark(r *http.Request, cfg config.Config, b bookmarks.Bookmark) apiBookmark {
	result := apiBookmark{
		ID: b.ID, URL: b.URL, Title: b.Title, Description: b.Description, Notes: b.Notes,
		IsArchived: b.IsArchived, Unread: b.Unread, Shared: b.Shared,
		TagNames:     append([]string{}, b.TagNames...),
		DateAdded:    b.DateAdded.UTC().Format("2006-01-02T15:04:05.000000Z"),
		DateModified: b.DateModified.UTC().Format("2006-01-02T15:04:05.000000Z"),
	}
	if b.WebArchiveSnapshotURL != "" {
		result.WebArchiveSnapshotURL = &b.WebArchiveSnapshotURL
	} else if b.URL != "" {
		fallback := "https://web.archive.org/web/" + b.DateAdded.UTC().Format("20060102150405") + "/" + b.URL
		result.WebArchiveSnapshotURL = &fallback
	}
	staticURL := func(file string) *string {
		if file == "" {
			return nil
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		absolute := (&url.URL{Scheme: scheme, Host: r.Host, Path: cfg.URLPrefix() + "static/" + strings.TrimPrefix(file, "/")}).String()
		return &absolute
	}
	result.FaviconURL = staticURL(b.FaviconFile)
	result.PreviewImageURL = staticURL(b.PreviewImageFile)
	return result
}

func serveBookmarksAPI(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository, metadataCache *metadata.Cache) {
	if !strings.HasPrefix(r.URL.Path, root) {
		writeNotFound(w, r)
		return
	}
	part := strings.TrimPrefix(r.URL.Path, root)
	list := part == "" || part == "archived/" || part == "shared/"
	archived := part == "archived/"
	shared := part == "shared/"
	check := part == "check/"
	singlefile := part == "singlefile/"
	action := ""
	assetPath := ""
	allow := "GET, PUT, PATCH, DELETE, HEAD, OPTIONS"
	if list {
		allow = "GET, POST, HEAD, OPTIONS"
		if archived || shared {
			allow = "GET, HEAD, OPTIONS"
		}
	} else if check {
		allow = "GET, HEAD, OPTIONS"
	} else if singlefile {
		allow = "POST, OPTIONS"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Allow", allow)
	w.Header().Set("Vary", "Accept, Accept-Language, Cookie")
	w.Header().Set("Content-Language", "en")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	var id int64
	invalidID := false
	if !list && !check && !singlefile {
		if !strings.HasSuffix(part, "/") {
			writeNotFound(w, r)
			return
		}
		segments := strings.Split(strings.TrimSuffix(part, "/"), "/")
		if len(segments) >= 2 && segments[1] == "assets" {
			assetPath = strings.Join(segments[2:], "/")
			assetSegments := strings.Split(assetPath, "/")
			if len(assetSegments) > 2 || len(assetSegments) == 2 && assetSegments[1] != "download" {
				writeNotFound(w, r)
				return
			}
			switch {
			case assetPath == "":
				w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			case assetPath == "upload":
				w.Header().Set("Allow", "POST, OPTIONS")
			case strings.HasSuffix(assetPath, "/download"):
				w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			default:
				w.Header().Set("Allow", "GET, DELETE, HEAD, OPTIONS")
			}
		} else if len(segments) == 2 && (segments[1] == "archive" || segments[1] == "unarchive") {
			action = segments[1]
			w.Header().Set("Allow", "POST, OPTIONS")
		} else if len(segments) != 1 {
			writeNotFound(w, r)
			return
		}
		var err error
		id, err = strconv.ParseInt(segments[0], 10, 64)
		invalidID = err != nil
	}
	token, present, parseErr := auth.ParseTokenAuthorization(r.Header.Get("Authorization"))
	if parseErr != nil {
		w.Header().Set("WWW-Authenticate", "Token")
		writeDetail(w, http.StatusUnauthorized, parseErr.Error())
		return
	}
	var user auth.User
	var err error
	if present {
		user, err = users.AuthenticateToken(r.Context(), token)
	} else if cookie, cookieErr := r.Cookie(auth.SessionCookieName); cookieErr == nil {
		user, err = users.AuthenticateSession(r.Context(), cookie.Value)
	} else {
		err = auth.ErrInvalidCredentials
	}
	if errors.Is(err, auth.ErrInvalidCredentials) && shared && !present && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		user = auth.User{}
		err = nil
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		w.Header().Set("WWW-Authenticate", "Token")
		if present {
			writeDetail(w, http.StatusUnauthorized, "Invalid token.")
		} else {
			writeDetail(w, http.StatusUnauthorized, "Authentication credentials were not provided.")
		}
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if r.Method == http.MethodOptions {
		switch {
		case part == "":
			writeAPIMetadata(w, "Bookmark List", http.MethodPost, bookmarkAPISchema)
		case archived:
			writeAPIMetadata(w, "Archived", "", "")
		case shared:
			writeAPIMetadata(w, "Shared", "", "")
		case check:
			writeAPIMetadata(w, "Check", "", "")
		case singlefile:
			writeAPIMetadata(w, "Singlefile", http.MethodPost, bookmarkAPISchema)
		case strings.Contains(part, "/assets/"):
			switch assetPath {
			case "":
				writeAPIMetadata(w, "Bookmark Asset List", "", "")
			case "upload":
				writeAPIMetadata(w, "Upload", http.MethodPost, assetUploadAPISchema)
			default:
				segments := strings.Split(assetPath, "/")
				if len(segments) > 2 || len(segments) == 2 && segments[1] != "download" {
					writeNotFound(w, r)
					return
				}
				if len(segments) == 2 {
					writeAPIMetadata(w, "Download", "", "")
				} else {
					writeAPIMetadata(w, "Bookmark Asset Instance", "", "")
				}
			}
		case action == "archive":
			writeAPIMetadata(w, "Archive", http.MethodPost, bookmarkAPISchema)
		case action == "unarchive":
			writeAPIMetadata(w, "Unarchive", http.MethodPost, bookmarkAPISchema)
		default:
			exists, lookupErr := ownedBookmarkExists(r, db, cfg.DBEngine, user.ID, id)
			if lookupErr != nil {
				writeDetail(w, http.StatusInternalServerError, "Server error")
				return
			}
			if exists {
				writeAPIMetadata(w, "Bookmark Instance", http.MethodPut, bookmarkAPISchema)
			} else {
				writeAPIMetadata(w, "Bookmark Instance", "", "")
			}
		}
		return
	}
	if invalidID {
		writeDetail(w, http.StatusNotFound, "Not found.")
		return
	}
	if singlefile {
		serveSingleFileUpload(w, r, cfg, db, user, repo, present, metadataCache)
		return
	}
	if assetPath != "" || strings.Contains(part, "/assets/") {
		serveBookmarkAssets(w, r, cfg, db, user, id, assetPath, present)
		return
	}
	if action != "" {
		if r.Method != http.MethodPost {
			writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
			return
		}
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		err := repo.SetArchived(r.Context(), user.ID, id, action == "archive")
		if errors.Is(err, sql.ErrNoRows) {
			writeDetail(w, http.StatusNotFound, "No Bookmark matches the given query.")
			return
		}
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		w.Header().Del("Content-Type")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if part == "" && r.Method == http.MethodPost {
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		serveBookmarkCreate(w, r, cfg, user, repo, metadataCache)
		return
	}
	if !list && (r.Method == http.MethodPut || r.Method == http.MethodPatch) {
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		serveBookmarkUpdate(w, r, cfg, user, repo, id)
		return
	}
	if !list && r.Method == http.MethodDelete {
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		serveBookmarkDelete(w, r, cfg, user, repo, id)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
		return
	}
	if check {
		serveBookmarkCheck(w, r, cfg, user, repo, metadataCache)
		return
	}
	if list {
		limit := positiveIntOr(r.URL.Query().Get("limit"), 100)
		offset := nonnegativeIntOr(r.URL.Query().Get("offset"), 0)
		options := bookmarks.ListOptionsFromValues(r.URL.Query(), archived, limit, offset)
		var items []bookmarks.Bookmark
		var count int64
		var err error
		if shared {
			items, count, err = repo.ListShared(r.Context(), user.ID, user.ID != 0, options)
		} else {
			items, count, err = repo.ListFiltered(r.Context(), user.ID, options)
		}
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		results := make([]apiBookmark, 0, len(items))
		for _, item := range items {
			results = append(results, serializeBookmark(r, cfg, item))
		}
		var next, previous *string
		if int64(offset)+int64(limit) < count {
			value := pageURL(r, limit, offset+limit)
			next = &value
		}
		if offset > 0 {
			previousOffset := max(0, offset-limit)
			value := pageURL(r, limit, previousOffset)
			previous = &value
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_ = writeJSON(w, struct {
				Count    int64         `json:"count"`
				Next     *string       `json:"next"`
				Previous *string       `json:"previous"`
				Results  []apiBookmark `json:"results"`
			}{count, next, previous, results})
		}
		return
	}
	item, err := repo.GetByID(r.Context(), user.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeDetail(w, http.StatusNotFound, "No Bookmark matches the given query.")
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_ = writeJSON(w, serializeBookmark(r, cfg, item))
	}
}

func positiveIntOr(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func nonnegativeIntOr(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func pageURL(r *http.Request, limit, offset int) string {
	copyURL := *r.URL
	query := copyURL.Query()
	query.Set("limit", strconv.Itoa(limit))
	if offset == 0 {
		query.Del("offset")
	} else {
		query.Set("offset", strconv.Itoa(offset))
	}
	copyURL.RawQuery = query.Encode()
	copyURL.Scheme = "http"
	if r.TLS != nil {
		copyURL.Scheme = "https"
	}
	copyURL.Host = r.Host
	return copyURL.String()
}
