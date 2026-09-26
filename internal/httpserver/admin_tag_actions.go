package httpserver

import (
	"database/sql"
	"embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_tag_delete_selected.html admin_sidebar.html
var adminTagDeleteSelectedFile embed.FS
var adminTagDeleteSelectedTemplate = adminSidebarTemplate(adminTagDeleteSelectedFile, "admin_tag_delete_selected.html")

type adminTagSelected struct {
	ID   int64
	Name string
}

type adminTagDeleteSelectedData struct {
	Language                            string
	Prefix, Username, Action, CSRFToken string
	Tags                                []adminTagSelected
	DashboardApps                       []adminDashboardApp
}

func serveAdminTagAction(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	if !permissions.canList() {
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
	if action != "delete_unused_tags" && action != "delete_selected" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}
	if action == "delete_selected" && !permissions.Delete {
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
	filteredQuery, args := adminTagListQuery(cfg.DBEngine, r.URL.Query().Get("q"), r.URL.Query().Get("owner__username"))
	selectionQuery := `SELECT id FROM (` + filteredQuery + `) AS filtered`
	if across == "0" {
		markers := make([]string, len(selectedIDs))
		for i, id := range selectedIDs {
			args = append(args, id)
			markers[i] = assetMarker(cfg.DBEngine, len(args))
		}
		selectionQuery += ` WHERE id IN (` + strings.Join(markers, ",") + `)`
	}
	if action == "delete_selected" {
		serveAdminTagDeleteSelected(w, r, cfg, db, user, selectionQuery, args)
		return
	}
	query := `DELETE FROM bookmarks_tag WHERE id IN (` + selectionQuery + `) AND NOT EXISTS (SELECT 1 FROM bookmarks_bookmark_tags AS bt WHERE bt.tag_id = bookmarks_tag.id)`
	result, err := db.ExecContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	message := "There were no unused tags in the selection"
	if count == 1 {
		message = "1 unused tag was successfully deleted."
	} else if count > 1 {
		message = fmt.Sprintf("%d unused tags were successfully deleted.", count)
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_admin_tag_action", message)
	writeRedirect(w, r, r.URL.RequestURI())
}

func serveAdminTagDeleteSelected(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, selectionQuery string, args []any) {
	rows, err := db.QueryContext(r.Context(), `SELECT id,name FROM bookmarks_tag WHERE id IN (`+selectionQuery+`) ORDER BY id`, args...)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	var tags []adminTagSelected
	for rows.Next() {
		var tag adminTagSelected
		if err := rows.Scan(&tag.ID, &tag.Name); err != nil {
			rows.Close()
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		tags = append(tags, tag)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if len(tags) == 0 {
		writeRedirect(w, r, r.URL.RequestURI())
		return
	}
	if r.PostForm.Get("post") != "yes" {
		data := adminTagDeleteSelectedData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.RequestURI(), CSRFToken: r.PostForm.Get("csrfmiddlewaretoken"), Tags: tags}
		models, err := loadAdminModels(r, db, cfg, user)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Language = selectedAdminLanguage(r).Code
		data.DashboardApps = groupAdminDashboardApps(cfg, models)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
		_ = adminTagDeleteSelectedTemplate.Execute(w, data)
		return
	}
	markers := make([]string, len(tags))
	values := make([]any, len(tags))
	for i, tag := range tags {
		markers[i] = assetMarker(cfg.DBEngine, i+1)
		values[i] = tag.ID
	}
	selected := `(` + strings.Join(markers, ",") + `)`
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	for _, tag := range tags {
		if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "tag", strconv.FormatInt(tag.ID, 10), tag.Name, 3, ""); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmark_tags WHERE tag_id IN `+selected, values...); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_tag WHERE id IN `+selected, values...); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	message := fmt.Sprintf("Successfully deleted %d tags.", len(tags))
	if len(tags) == 1 {
		message = "Successfully deleted 1 tag."
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_admin_tag_action", message)
	writeRedirect(w, r, r.URL.RequestURI())
}
