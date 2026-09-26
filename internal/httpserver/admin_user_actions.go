package httpserver

import (
	"database/sql"
	"embed"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_user_delete_selected.html admin_sidebar.html
var adminUserDeleteSelectedFile embed.FS
var adminUserDeleteSelectedTemplate = adminSidebarTemplate(adminUserDeleteSelectedFile, "admin_user_delete_selected.html")

type adminUserSelected struct {
	ID       int64
	Username string
}

type adminUserDeleteSelectedData struct {
	Language                            string
	Prefix, Username, Action, CSRFToken string
	Users                               []adminUserSelected
	Summary                             []adminDeletionSummary
	Nodes                               []adminDeletionNode
	DashboardApps                       []adminDashboardApp
}

func serveAdminUserAction(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, actor auth.User, permissions adminPermissions) {
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
	if r.PostForm.Get("action") != "delete_selected" {
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
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
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
	filteredQuery, args := adminUserListQuery(cfg.DBEngine, r.URL.Query().Get("q"), r.URL.Query())
	selectionQuery := `SELECT id FROM (` + filteredQuery + `) AS filtered`
	if across == "0" {
		markers := make([]string, len(selectedIDs))
		for i, id := range selectedIDs {
			args = append(args, id)
			markers[i] = assetMarker(cfg.DBEngine, len(args))
		}
		selectionQuery += ` WHERE id IN (` + strings.Join(markers, ",") + `)`
	}
	rows, err := db.QueryContext(r.Context(), `SELECT id,username FROM auth_user WHERE id IN (`+selectionQuery+`) ORDER BY id`, args...)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	var users []adminUserSelected
	for rows.Next() {
		var selectedUser adminUserSelected
		if err := rows.Scan(&selectedUser.ID, &selectedUser.Username); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		users = append(users, selectedUser)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if len(users) == 0 {
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
		return
	}
	if r.PostForm.Get("post") != "yes" {
		data := adminUserDeleteSelectedData{Prefix: cfg.URLPrefix(), Username: actor.Username, Action: r.URL.RequestURI(), CSRFToken: r.PostForm.Get("csrfmiddlewaretoken"), Users: users}
		data.Summary, data.Nodes, err = adminUserDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), users)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		models, err := loadAdminModels(r, db, cfg, actor)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Language = selectedAdminLanguage(r).Code
		localizeAdminDeletionGraph(data.Language, data.Summary, data.Nodes)
		data.DashboardApps = groupAdminDashboardApps(cfg, models)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
		_ = adminUserDeleteSelectedTemplate.Execute(w, data)
		return
	}
	ids := make([]int64, len(users))
	for i, selectedUser := range users {
		ids[i] = selectedUser.ID
	}
	files, err := deleteAdminUsersData(r, cfg, db, actor.ID, ids)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	for _, name := range files.Previews {
		removeStoredFile(filepath.Join(cfg.DataDir, "previews"), name)
	}
	for _, name := range files.Assets {
		removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
	}
	message := fmt.Sprintf("Successfully deleted %d users.", len(users))
	if len(users) == 1 {
		message = "Successfully deleted 1 user."
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_admin_user_action", message)
	http.Redirect(w, r, r.URL.RequestURI(), http.StatusFound)
}
