package httpserver

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpclient"
	"github.com/juev/linkding/internal/metadata"
)

type bookmarkCreateRequest struct {
	URL          *string    `json:"url"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Notes        string     `json:"notes"`
	Unread       bool       `json:"unread"`
	Shared       bool       `json:"shared"`
	IsArchived   bool       `json:"is_archived"`
	TagNames     []string   `json:"tag_names"`
	DateAdded    *time.Time `json:"date_added"`
	DateModified *time.Time `json:"date_modified"`
}

func serveBookmarkCreate(w http.ResponseWriter, r *http.Request, cfg config.Config, user auth.User, repo *bookmarks.Repository, metadataCache *metadata.Cache) {
	limit := cfg.RequestMaxContentLength
	if limit <= 0 {
		limit = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var input bookmarkCreateRequest
	validation, err := decodeDRFJSONObject(r.Body, &input,
		[]string{"url", "title", "description", "notes"}, nil, []string{"tag_names"})
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = writeJSON(w, map[string][]string{"detail": {"JSON parse error."}})
		return
	}
	if validation != nil {
		writeFieldError(w, validation.field, validation.message)
		return
	}
	if input.URL == nil {
		writeFieldError(w, "url", "This field is required.")
		return
	}
	if *input.URL == "" {
		writeFieldError(w, "url", "This field may not be blank.")
		return
	}
	if len([]rune(*input.URL)) > 2048 {
		writeFieldError(w, "url", "Ensure this field has no more than 2048 characters.")
		return
	}
	if len([]rune(input.Title)) > 512 {
		writeFieldError(w, "title", "Ensure this field has no more than 512 characters.")
		return
	}
	if !cfg.DisableURLValidation && !validBookmarkURL(*input.URL) {
		writeFieldError(w, "url", "Enter a valid URL.")
		return
	}
	bookmark, _, err := repo.CreateOrUpdateData(r.Context(), user.ID, bookmarks.CreateInput{
		URL: *input.URL, Title: input.Title, Description: input.Description, Notes: input.Notes,
		Unread: input.Unread, Shared: input.Shared, IsArchived: input.IsArchived,
		TagNames: input.TagNames, DateAdded: input.DateAdded, DateModified: input.DateModified,
		DisableHTMLSnapshot: r.URL.Query().Has("disable_html_snapshot"),
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if _, disabled := r.URL.Query()["disable_scraping"]; !disabled {
		client := httpclient.New(cfg.AllowedInternalHosts, 10*time.Second)
		meta := metadataCache.Load(r.Context(), client, bookmark.URL, false)
		bookmark, err = repo.EnhanceMetadata(r.Context(), user.ID, bookmark.ID, meta.Title, meta.Description)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
	}
	w.Header().Set("Location", bookmark.URL)
	w.WriteHeader(http.StatusCreated)
	_ = writeJSON(w, serializeBookmark(r, cfg, bookmark))
}

func writeFieldError(w http.ResponseWriter, field, message string) {
	w.WriteHeader(http.StatusBadRequest)
	_ = writeJSON(w, map[string][]string{field: {message}})
}

func validBookmarkURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "ftps":
		return true
	default:
		return false
	}
}
