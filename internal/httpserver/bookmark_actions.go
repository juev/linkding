package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/jobs"
)

func serveBookmarkAction(w http.ResponseWriter, r *http.Request, path string, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	shared := strings.Contains(path, "/shared/")
	archived := strings.Contains(path, "/archived/")
	respond := func() {
		if strings.Contains(r.Header.Get("Accept"), "text/vnd.turbo-stream.html") {
			serveBookmarkActionStream(w, r, cfg, db, users, repo, archived, shared)
			return
		}
		redirectBookmarkAction(w, r, cfg, archived, shared)
	}
	if r.Method != http.MethodPost {
		redirectBookmarkAction(w, r, cfg, archived, shared)
		return
	}
	var parseErr error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		parseErr = r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		parseErr = r.ParseForm()
	}
	if parseErr != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		writeCSRFFailure(w, r)
		return
	}
	if raw, ok := r.PostForm["remove_asset"]; ok && len(raw) > 0 {
		assetID, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || assetID <= 0 {
			writeNotFound(w, r)
			return
		}
		query := `SELECT a.id,a.bookmark_id,a.date_created,a.file_size,a.asset_type,a.content_type,a.display_name,a.status,a.file,a.gzip FROM bookmarks_bookmarkasset a JOIN bookmarks_bookmark b ON b.id=a.bookmark_id WHERE a.id = ` + assetMarker(cfg.DBEngine, 1) + ` AND b.owner_id = ` + assetMarker(cfg.DBEngine, 2)
		asset, err := scanAsset(db.QueryRowContext(r.Context(), query, assetID, user.ID))
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil || deleteAsset(r, cfg, db, asset.Bookmark, asset) != nil {
			http.Error(w, "Server error", 500)
			return
		}
		respond()
		return
	}
	if raw, ok := r.PostForm["update_state"]; ok && len(raw) > 0 && r.PostForm.Get("upload_asset") == "" && r.PostForm.Get("create_html_snapshot") == "" {
		id, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || id <= 0 {
			writeNotFound(w, r)
			return
		}
		query := `UPDATE bookmarks_bookmark SET is_archived = ` + assetMarker(cfg.DBEngine, 1) + `, unread = ` + assetMarker(cfg.DBEngine, 2) + `, shared = ` + assetMarker(cfg.DBEngine, 3) + `, date_modified = ` + assetMarker(cfg.DBEngine, 4) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 5) + ` AND owner_id = ` + assetMarker(cfg.DBEngine, 6)
		result, err := db.ExecContext(r.Context(), query, r.PostForm.Get("is_archived") == "on", r.PostForm.Get("unread") == "on", r.PostForm.Get("shared") == "on", time.Now().UTC(), id, user.ID)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			writeNotFound(w, r)
			return
		}
		respond()
		return
	}
	if raw, ok := r.PostForm["upload_asset"]; ok && len(raw) > 0 {
		if cfg.DisableAssetUpload {
			http.Error(w, "Asset upload is disabled", 403)
			return
		}
		id, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || id <= 0 {
			writeNotFound(w, r)
			return
		}
		if _, err = repo.GetByID(r.Context(), user.ID, id); errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		file, header, err := r.FormFile("upload_asset_file")
		if err != nil {
			http.Error(w, "No file provided", 400)
			return
		}
		defer file.Close()
		if _, err := storeUploadedAsset(r.Context(), cfg, db, id, file, header); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		respond()
		return
	}
	if _, bulk := r.PostForm["bulk_execute"]; bulk && shared {
		http.Error(w, "View does not support bulk actions", 400)
		return
	}
	for _, action := range []string{"archive", "unarchive", "remove", "mark_as_read", "unshare", "create_html_snapshot"} {
		if raw := r.PostForm.Get(action); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				writeNotFound(w, r)
				return
			}
			if _, err := repo.GetByID(r.Context(), user.ID, id); errors.Is(err, sql.ErrNoRows) {
				writeNotFound(w, r)
				return
			} else if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			switch action {
			case "archive":
				err = repo.SetArchived(r.Context(), user.ID, id, true)
			case "unarchive":
				err = repo.SetArchived(r.Context(), user.ID, id, false)
			case "mark_as_read":
				err = repo.SetState(r.Context(), user.ID, []int64{id}, "unread", false)
			case "unshare":
				err = repo.SetState(r.Context(), user.ID, []int64{id}, "shared", false)
			case "remove":
				var files bookmarks.DeletedFiles
				files, err = repo.DeleteData(r.Context(), user.ID, id)
				if err == nil {
					removeStoredFile(filepath.Join(cfg.DataDir, "previews"), files.Preview)
					for _, name := range files.Assets {
						removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
					}
				}
			case "create_html_snapshot":
				if cfg.EnableSnapshots && !cfg.DisableBackgroundTasks {
					err = jobs.EnqueueSnapshot(r.Context(), db, cfg.DBEngine, id)
				}
			}
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			respond()
			return
		}
	}
	if _, bulk := r.PostForm["bulk_execute"]; bulk {
		ids := make([]int64, 0)
		if r.PostForm.Get("bulk_select_across") == "on" {
			options := bookmarks.ListOptionsFromValues(r.URL.Query(), archived, 1, 0)
			_, total, err := repo.ListFiltered(r.Context(), user.ID, options)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if total > 0 {
				options.Limit = int(total)
				all, _, err := repo.ListFiltered(r.Context(), user.ID, options)
				if err != nil {
					http.Error(w, "Server error", 500)
					return
				}
				for _, item := range all {
					ids = append(ids, item.ID)
				}
			}
		} else {
			for _, raw := range r.PostForm["bookmark_id"] {
				if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
					ids = append(ids, id)
				}
			}
		}
		owned := make([]int64, 0, len(ids))
		seen := map[int64]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, err := repo.GetByID(r.Context(), user.ID, id); err == nil {
				owned = append(owned, id)
			} else if !errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Server error", 500)
				return
			}
		}
		ids = owned
		var err error
		switch r.PostForm.Get("bulk_action") {
		case "bulk_archive":
			err = repo.SetState(r.Context(), user.ID, ids, "is_archived", true)
		case "bulk_unarchive":
			err = repo.SetState(r.Context(), user.ID, ids, "is_archived", false)
		case "bulk_read":
			err = repo.SetState(r.Context(), user.ID, ids, "unread", false)
		case "bulk_unread":
			err = repo.SetState(r.Context(), user.ID, ids, "unread", true)
		case "bulk_share":
			err = repo.SetState(r.Context(), user.ID, ids, "shared", true)
		case "bulk_unshare":
			err = repo.SetState(r.Context(), user.ID, ids, "shared", false)
		case "bulk_tag":
			err = repo.TagMany(r.Context(), user.ID, ids, r.PostForm.Get("bulk_tag_string"), true)
		case "bulk_untag":
			err = repo.TagMany(r.Context(), user.ID, ids, r.PostForm.Get("bulk_tag_string"), false)
		case "bulk_delete":
			for _, id := range ids {
				files, deleteErr := repo.DeleteData(r.Context(), user.ID, id)
				if errors.Is(deleteErr, sql.ErrNoRows) {
					continue
				}
				if deleteErr != nil {
					err = deleteErr
					break
				}
				removeStoredFile(filepath.Join(cfg.DataDir, "previews"), files.Preview)
				for _, name := range files.Assets {
					removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
				}
			}
		case "bulk_snapshot":
			if cfg.EnableSnapshots && !cfg.DisableBackgroundTasks {
				for _, id := range ids {
					if err = jobs.EnqueueSnapshot(r.Context(), db, cfg.DBEngine, id); err != nil {
						break
					}
				}
			}
		case "bulk_refresh":
			if !cfg.DisableBackgroundTasks {
				for _, id := range ids {
					payload, _ := json.Marshal(struct {
						BookmarkID int64 `json:"bookmark_id"`
					}{id})
					if _, err = jobs.New(db, cfg.DBEngine).Enqueue(r.Context(), "refresh_metadata", payload); err != nil {
						break
					}
					if _, err = jobs.New(db, cfg.DBEngine).Enqueue(r.Context(), "load_preview_image", payload); err != nil {
						break
					}
				}
			}
		default:
			http.Error(w, "Invalid bulk action", 400)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	respond()
}

func redirectBookmarkAction(w http.ResponseWriter, r *http.Request, cfg config.Config, archived, shared bool) {
	destination := cfg.URLPrefix() + "bookmarks"
	if archived {
		destination += "/archived"
	}
	if shared {
		destination += "/shared"
	}
	if r.URL.RawQuery != "" {
		destination += "?" + r.URL.RawQuery
	}
	writeRedirect(w, r, destination)
}
