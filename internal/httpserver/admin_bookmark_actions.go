package httpserver

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
)

type adminSelectedBookmark struct {
	ID, OwnerID int64
	URL         string
}

func serveAdminBookmarkAction(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, permissions adminPermissions) {
	if !permissions.canList() || !permissions.Delete {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}
	action := r.PostForm.Get("action")
	switch action {
	case "delete_selected_bookmarks", "archive_selected_bookmarks", "unarchive_selected_bookmarks", "mark_as_read", "mark_as_unread":
	default:
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}
	across := r.PostForm.Get("select_across")
	if across != "0" && across != "1" {
		http.Error(w, "Invalid selection", http.StatusBadRequest)
		return
	}
	selected := r.PostForm["_selected_action"]
	if len(selected) == 0 {
		writeRedirect(w, r, r.URL.RequestURI())
		return
	}
	selectedIDs := make([]int64, 0, len(selected))
	for _, raw := range selected {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid selection", http.StatusBadRequest)
			return
		}
		selectedIDs = append(selectedIDs, id)
	}
	filteredQuery, args := adminBookmarkListQuery(cfg.DBEngine, r.URL.Query().Get("q"), r.URL.Query())
	selectionQuery := `SELECT id FROM (` + filteredQuery + `) AS filtered`
	if across == "0" {
		markers := make([]string, len(selectedIDs))
		for i, id := range selectedIDs {
			args = append(args, id)
			markers[i] = assetMarker(cfg.DBEngine, len(args))
		}
		selectionQuery += ` WHERE id IN (` + strings.Join(markers, ",") + `)`
	}
	rows, err := db.QueryContext(r.Context(), `SELECT id,owner_id,url FROM bookmarks_bookmark WHERE id IN (`+selectionQuery+`) ORDER BY id`, args...)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	var selectedBookmarks []adminSelectedBookmark
	for rows.Next() {
		var item adminSelectedBookmark
		if err := rows.Scan(&item.ID, &item.OwnerID, &item.URL); err != nil {
			rows.Close()
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		selectedBookmarks = append(selectedBookmarks, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	count := len(selectedBookmarks)
	if count == 0 {
		writeRedirect(w, r, r.URL.RequestURI())
		return
	}
	marker := func(i int) string { return assetMarker(cfg.DBEngine, i) }
	var message string
	switch action {
	case "delete_selected_bookmarks":
		repo := bookmarks.NewRepository(db, cfg.DBEngine)
		for _, item := range selectedBookmarks {
			files, err := repo.DeleteData(r.Context(), item.OwnerID, item.ID)
			if err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			removeStoredFile(filepath.Join(cfg.DataDir, "previews"), files.Preview)
			for _, name := range files.Assets {
				removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
			}
		}
		message = adminBookmarkActionMessage(count, "bookmark was successfully deleted.", "bookmarks were successfully deleted.")
	case "archive_selected_bookmarks", "unarchive_selected_bookmarks":
		archived := action == "archive_selected_bookmarks"
		for _, item := range selectedBookmarks {
			query := `UPDATE bookmarks_bookmark SET is_archived = ` + marker(1) + `, date_modified = ` + marker(2) + `, url_normalized = ` + marker(3) + ` WHERE id = ` + marker(4)
			if _, err := db.ExecContext(r.Context(), query, archived, time.Now().UTC(), bookmarks.NormalizeURL(item.URL), item.ID); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
		}
		if archived {
			message = adminBookmarkActionMessage(count, "bookmark was successfully archived.", "bookmarks were successfully archived.")
		} else {
			message = adminBookmarkActionMessage(count, "bookmark was successfully unarchived.", "bookmarks were successfully unarchived.")
		}
	case "mark_as_read", "mark_as_unread":
		unread := action == "mark_as_unread"
		state := "FALSE"
		if unread {
			state = "TRUE"
		}
		query := `UPDATE bookmarks_bookmark SET unread = ` + state + ` WHERE id IN (` + selectionQuery + `)`
		if _, err := db.ExecContext(r.Context(), query, args...); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		if unread {
			message = adminBookmarkActionMessage(count, "bookmark marked as unread.", "bookmarks marked as unread.")
		} else {
			message = adminBookmarkActionMessage(count, "bookmark marked as read.", "bookmarks marked as read.")
		}
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_admin_bookmark_action", message)
	writeRedirect(w, r, r.URL.RequestURI())
}

func adminBookmarkActionMessage(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
