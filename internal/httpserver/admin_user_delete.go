package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_user_delete.html admin_sidebar.html
var adminUserDeleteFile embed.FS
var adminUserDeleteTemplate = adminSidebarTemplate(adminUserDeleteFile, "admin_user_delete.html")

type adminUserDeleteData struct {
	Language                                                    string
	Prefix, Title, Username, Target, CSRFToken, Action, ListURL string
	DashboardApps                                               []adminDashboardApp
}

func serveAdminUserDelete(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, actor auth.User, permissions adminPermissions, id int64) {
	if !permissions.Delete {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var target string
	query := `SELECT username FROM auth_user WHERE id = ` + assetMarker(cfg.DBEngine, 1)
	err := db.QueryRowContext(r.Context(), query, id).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	base := cfg.URLPrefix() + "admin/auth/user/"
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			http.Error(w, "CSRF verification failed", 403)
			return
		}
		if r.PostForm.Get("post") != "yes" {
			http.Error(w, "Invalid form", 400)
			return
		}
		files, err := deleteAdminUserData(r, cfg, db, actor.ID, id)
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
		http.Redirect(w, r, base, http.StatusFound)
		return
	}
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
	masked, err := auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := adminUserDeleteData{Prefix: cfg.URLPrefix(), Title: "Delete user", Username: actor.Username, Target: target, CSRFToken: masked, Action: r.URL.Path, ListURL: base}
	models, err := loadAdminModels(r, db, cfg, actor)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminUserDeleteTemplate.Execute(w, data)
	}
}

type adminUserDeletedFiles struct {
	Previews []string
	Assets   []string
}

func deleteAdminUserData(r *http.Request, cfg config.Config, db *sql.DB, actorID, id int64) (adminUserDeletedFiles, error) {
	return deleteAdminUsersData(r, cfg, db, actorID, []int64{id})
}

func deleteAdminUsersData(r *http.Request, cfg config.Config, db *sql.DB, actorID int64, ids []int64) (adminUserDeletedFiles, error) {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return adminUserDeletedFiles{}, err
	}
	defer tx.Rollback()
	var files adminUserDeletedFiles
	for _, id := range ids {
		var username string
		if err := tx.QueryRowContext(r.Context(), `SELECT username FROM auth_user WHERE id=`+assetMarker(cfg.DBEngine, 1), id).Scan(&username); err != nil {
			return files, err
		}
		if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actorID, "auth", "user", strconv.FormatInt(id, 10), username, 3, ""); err != nil {
			return files, err
		}
	}
	for _, id := range ids {
		item, err := deleteAdminUserDataTx(r, cfg, tx, id)
		if err != nil {
			return files, err
		}
		files.Previews = append(files.Previews, item.Previews...)
		files.Assets = append(files.Assets, item.Assets...)
	}
	if err := tx.Commit(); err != nil {
		return files, err
	}
	return files, nil
}

func deleteAdminUserDataTx(r *http.Request, cfg config.Config, tx *sql.Tx, id int64) (adminUserDeletedFiles, error) {
	var files adminUserDeletedFiles
	for _, source := range []struct {
		Query string
		Names *[]string
	}{
		{`SELECT preview_image_file FROM bookmarks_bookmark WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1), &files.Previews},
		{`SELECT a.file FROM bookmarks_bookmarkasset AS a JOIN bookmarks_bookmark AS b ON b.id=a.bookmark_id WHERE b.owner_id = ` + assetMarker(cfg.DBEngine, 1), &files.Assets},
	} {
		rows, err := tx.QueryContext(r.Context(), source.Query, id)
		if err != nil {
			return files, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return files, err
			}
			if name != "" {
				*source.Names = append(*source.Names, name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return files, err
		}
	}
	queries := []string{
		`UPDATE bookmarks_globalsettings SET guest_profile_user_id=NULL WHERE guest_profile_user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id=` + assetMarker(cfg.DBEngine, 1) + `) OR tag_id IN (SELECT id FROM bookmarks_tag WHERE owner_id=` + assetMarker(cfg.DBEngine, 2) + `)`,
		`UPDATE bookmarks_bookmark SET latest_snapshot_id=NULL WHERE latest_snapshot_id IN (SELECT a.id FROM bookmarks_bookmarkasset AS a JOIN bookmarks_bookmark AS b ON b.id=a.bookmark_id WHERE b.owner_id=` + assetMarker(cfg.DBEngine, 1) + `)`,
		`DELETE FROM bookmarks_bookmarkasset WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id=` + assetMarker(cfg.DBEngine, 1) + `)`,
		`DELETE FROM bookmarks_bookmark WHERE owner_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_tag WHERE owner_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_bookmarkbundle WHERE owner_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_apitoken WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_feedtoken WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM authtoken_token WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_toast WHERE owner_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM auth_user_groups WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM auth_user_user_permissions WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM django_admin_log WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
		`DELETE FROM bookmarks_userprofile WHERE user_id=` + assetMarker(cfg.DBEngine, 1),
	}
	for index, query := range queries {
		var args []any
		if index == 1 {
			args = []any{id, id}
		} else {
			args = []any{id}
		}
		if _, err := tx.ExecContext(r.Context(), query, args...); err != nil {
			return files, err
		}
	}
	result, err := tx.ExecContext(r.Context(), `DELETE FROM auth_user WHERE id=`+assetMarker(cfg.DBEngine, 1), id)
	if err != nil {
		return files, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return files, err
	}
	if rows != 1 {
		return files, sql.ErrNoRows
	}
	return files, nil
}
