package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
)

type bookmarkUpdateRequest struct {
	URL         *string    `json:"url"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Notes       *string    `json:"notes"`
	Unread      *bool      `json:"unread"`
	Shared      *bool      `json:"shared"`
	IsArchived  *bool      `json:"is_archived"`
	TagNames    *[]string  `json:"tag_names"`
	DateAdded   *time.Time `json:"date_added"`
}

func serveBookmarkUpdate(w http.ResponseWriter, r *http.Request, cfg config.Config, user auth.User, repo *bookmarks.Repository, id int64) {
	limit := cfg.RequestMaxContentLength
	if limit <= 0 {
		limit = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var input bookmarkUpdateRequest
	validation, err := decodeDRFJSONObject(r.Body, &input,
		[]string{"url", "title", "description", "notes"}, nil, []string{"tag_names"})
	if err != nil {
		writeDetail(w, http.StatusBadRequest, drfJSONErrorDetail(err))
		return
	}
	if validation != nil {
		writeFieldError(w, validation.field, validation.message)
		return
	}
	if r.Method == http.MethodPut && input.URL == nil {
		writeFieldError(w, "url", "This field is required.")
		return
	}
	if input.URL != nil {
		if *input.URL == "" {
			writeFieldError(w, "url", "This field may not be blank.")
			return
		}
		if len([]rune(*input.URL)) > 2048 {
			writeFieldError(w, "url", "Ensure this field has no more than 2048 characters.")
			return
		}
		if !cfg.DisableURLValidation && !validBookmarkURL(*input.URL) {
			writeFieldError(w, "url", "Enter a valid URL.")
			return
		}
	}
	if input.Title != nil && len([]rune(*input.Title)) > 512 {
		writeFieldError(w, "title", "Ensure this field has no more than 512 characters.")
		return
	}
	item, err := repo.UpdateData(r.Context(), user.ID, id, bookmarks.UpdateInput{
		URL: input.URL, Title: input.Title, Description: input.Description, Notes: input.Notes,
		Unread: input.Unread, Shared: input.Shared, IsArchived: input.IsArchived,
		TagNames: input.TagNames, DateAdded: input.DateAdded, ExactURLDuplicateCheck: true,
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeDetail(w, http.StatusNotFound, "No Bookmark matches the given query.")
		return
	}
	if errors.Is(err, bookmarks.ErrDuplicateURL) {
		writeFieldError(w, "url", "A bookmark with this URL already exists.")
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = writeJSON(w, serializeBookmark(r, cfg, item))
}
