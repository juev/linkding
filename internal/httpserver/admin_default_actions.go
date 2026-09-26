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

//go:embed admin_default_delete_selected.html admin_sidebar.html
var adminDefaultDeleteSelectedFile embed.FS
var adminDefaultDeleteSelectedTemplate = adminSidebarTemplate(adminDefaultDeleteSelectedFile, "admin_default_delete_selected.html")

type adminDefaultSelected struct {
	ID, Repr, File string
}

type adminDefaultDeleteSelectedData struct {
	Language                                           string
	Prefix, Username, Action, CSRFToken, Model, Plural string
	ModelSlug, Singular                                string
	Objects                                            []adminDefaultSelected
	DashboardApps                                      []adminDashboardApp
}

func serveAdminDefaultAction(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, actor auth.User, definition adminModelDefinition, permissions adminPermissions) {
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
		writeRedirect(w, r, r.URL.RequestURI())
		return
	}
	if definition.Model != "feedtoken" {
		for _, id := range selected {
			parsed, err := strconv.ParseInt(id, 10, 64)
			if err != nil || parsed <= 0 {
				http.Error(w, "Invalid selection", http.StatusBadRequest)
				return
			}
		}
	}
	query, args, err := adminDefaultSelectionQuery(cfg.DBEngine, definition.Model, r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("owner__username"), r.URL.Query().Get("user__username"))
	if err != nil {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}
	if across == "0" {
		markers := make([]string, len(selected))
		for i, id := range selected {
			args = append(args, id)
			markers[i] = assetMarker(cfg.DBEngine, len(args))
		}
		query += ` WHERE id IN (` + strings.Join(markers, ",") + `)`
	}
	objects, err := loadAdminDefaultSelection(r, cfg, db, definition.Model, query, args)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	if len(objects) == 0 {
		writeRedirect(w, r, r.URL.RequestURI())
		return
	}
	if r.PostForm.Get("post") != "yes" {
		data := adminDefaultDeleteSelectedData{
			Prefix: cfg.URLPrefix(), Username: actor.Username, Action: r.URL.RequestURI(), CSRFToken: r.PostForm.Get("csrfmiddlewaretoken"),
			Model: definition.Label, Plural: definition.Plural, Objects: objects,
			ModelSlug: definition.Model, Singular: strings.ToLower(definition.Label),
		}
		if definition.Model == "apitoken" {
			data.Model = "Api token"
			data.Plural = "Api tokens"
		}
		models, err := loadAdminModels(r, db, cfg, actor)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		data.Language = selectedAdminLanguage(r).Code
		data.DashboardApps = groupAdminDashboardApps(cfg, models)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
		_ = adminDefaultDeleteSelectedTemplate.Execute(w, data)
		return
	}
	if err := deleteAdminDefaultSelection(r, cfg, db, actor.ID, definition.Model, objects); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	message := fmt.Sprintf("Successfully deleted %d %s.", len(objects), strings.ToLower(definition.Plural))
	if len(objects) == 1 {
		message = "Successfully deleted 1 " + strings.ToLower(definition.Label) + "."
	}
	settingsFlash(w, cfg.URLPrefix(), "ld_admin_default_action", message)
	writeRedirect(w, r, r.URL.RequestURI())
}

func adminDefaultSelectionQuery(engine, model, search, status, owner, user string) (string, []any, error) {
	var base string
	var args []any
	switch model {
	case "bookmarkasset":
		base, args = adminAssetListQuery(engine, search, status)
	case "bookmarkbundle":
		base, args = adminBundleListQuery(engine, search, owner)
	case "toast":
		base, args = adminToastListQuery(engine, search, owner)
	case "apitoken":
		base, args = adminAPITokenListQuery(engine, search, user)
	case "feedtoken":
		base, args = adminFeedTokenListQuery(engine, search, user)
	default:
		return "", nil, fmt.Errorf("unsupported admin model %q", model)
	}
	return `SELECT id FROM (` + base + `) AS filtered`, args, nil
}

func loadAdminDefaultSelection(r *http.Request, cfg config.Config, db *sql.DB, model, selectionQuery string, args []any) ([]adminDefaultSelected, error) {
	var query string
	switch model {
	case "bookmarkasset":
		query = `SELECT a.id,COALESCE(NULLIF(a.display_name,''),'Bookmark Asset #' || a.id),a.file FROM bookmarks_bookmarkasset AS a WHERE a.id IN (` + selectionQuery + `) ORDER BY a.id`
	case "bookmarkbundle":
		query = `SELECT b.id,b.name,'' FROM bookmarks_bookmarkbundle AS b WHERE b.id IN (` + selectionQuery + `) ORDER BY b.id`
	case "toast":
		query = `SELECT t.id,'Toast object (' || t.id || ')','' FROM bookmarks_toast AS t WHERE t.id IN (` + selectionQuery + `) ORDER BY t.id`
	case "apitoken":
		query = `SELECT t.id,t.name || ' (' || u.username || ')','' FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id WHERE t.id IN (` + selectionQuery + `) ORDER BY t.id`
	case "feedtoken":
		query = `SELECT t.key,t.key,'' FROM bookmarks_feedtoken AS t WHERE t.key IN (` + selectionQuery + `) ORDER BY t.key`
	default:
		return nil, fmt.Errorf("unsupported admin model %q", model)
	}
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []adminDefaultSelected
	for rows.Next() {
		var object adminDefaultSelected
		if err := rows.Scan(&object.ID, &object.Repr, &object.File); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func deleteAdminDefaultSelection(r *http.Request, cfg config.Config, db *sql.DB, actorID int64, model string, objects []adminDefaultSelected) error {
	var table, key string
	switch model {
	case "bookmarkasset":
		table, key = "bookmarks_bookmarkasset", "id"
	case "bookmarkbundle":
		table, key = "bookmarks_bookmarkbundle", "id"
	case "toast":
		table, key = "bookmarks_toast", "id"
	case "apitoken":
		table, key = "bookmarks_apitoken", "id"
	case "feedtoken":
		table, key = "bookmarks_feedtoken", "key"
	default:
		return fmt.Errorf("unsupported admin model %q", model)
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	markers := make([]string, len(objects))
	args := make([]any, len(objects))
	for i, object := range objects {
		if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actorID, "bookmarks", model, object.ID, object.Repr, 3, ""); err != nil {
			return err
		}
		markers[i] = assetMarker(cfg.DBEngine, i+1)
		args[i] = object.ID
	}
	selected := `(` + strings.Join(markers, ",") + `)`
	if model == "bookmarkasset" {
		if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks_bookmark SET latest_snapshot_id=NULL WHERE latest_snapshot_id IN `+selected, args...); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE `+key+` IN `+selected, args...); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if model == "bookmarkasset" {
		for _, object := range objects {
			removeStoredFile(filepath.Join(cfg.DataDir, "assets"), object.File)
		}
	}
	return nil
}
