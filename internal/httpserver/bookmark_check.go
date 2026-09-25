package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpclient"
	"github.com/juev/linkding/internal/metadata"
)

type apiMetadata struct {
	URL          *string `json:"url"`
	Title        *string `json:"title"`
	Description  *string `json:"description"`
	PreviewImage *string `json:"preview_image"`
}

type apiCheckResult struct {
	Bookmark *apiBookmark `json:"bookmark"`
	Metadata apiMetadata  `json:"metadata"`
	AutoTags []string     `json:"auto_tags"`
}

func serveBookmarkCheck(w http.ResponseWriter, r *http.Request, cfg config.Config, user auth.User, repo *bookmarks.Repository) {
	values, hasURL := r.URL.Query()["url"]
	var requestedURL string
	if hasURL && len(values) > 0 {
		requestedURL = values[0]
	}
	var bookmark *apiBookmark
	if hasURL {
		item, err := repo.FindExisting(r.Context(), user.ID, requestedURL)
		if err == nil {
			serialized := serializeBookmark(r, cfg, item)
			bookmark = &serialized
		} else if !errors.Is(err, sql.ErrNoRows) {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
	}
	var metaURL, title, description, previewImage *string
	if hasURL {
		metaURL = &requestedURL
		client := httpclient.New(cfg.AllowedInternalHosts, 10*time.Second)
		meta := metadata.Load(r.Context(), client, requestedURL)
		if meta.Title != "" {
			title = &meta.Title
		}
		if meta.Description != "" {
			description = &meta.Description
		}
		if meta.PreviewImage != "" {
			previewImage = &meta.PreviewImage
		}
	}
	tags, err := repo.AutoTagsForURL(r.Context(), user.ID, requestedURL)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if tags == nil {
		tags = []string{}
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_ = writeJSON(w, apiCheckResult{Bookmark: bookmark,
			Metadata: apiMetadata{metaURL, title, description, previewImage}, AutoTags: tags})
	}
}
