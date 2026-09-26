package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_bookmark_asset.html admin_sidebar.html
var adminBookmarkAssetFile embed.FS
var adminBookmarkAssetTemplate = adminSidebarTemplate(adminBookmarkAssetFile, "admin_bookmark_asset.html")

type adminAssetBookmarkOption struct {
	ID       int64
	Label    string
	Selected bool
}

type adminBookmarkAssetData struct {
	Language                                            string
	DashboardApps                                       []adminDashboardApp
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	File, FileSize, AssetType, ContentType              string
	DisplayName, ObjectName, Status, Error              string
	ID, BookmarkID                                      int64
	Gzip, ConfirmDelete, CanChange, CanDelete           bool
	Bookmarks                                           []adminAssetBookmarkOption
	Deletion                                            adminSingleDeletion
}

func serveAdminBookmarkAsset(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/bookmarkasset/"
	part := strings.TrimPrefix(r.URL.Path, base)
	var action string
	var id int64
	if part == "add/" {
		action = "add"
	} else {
		pieces := strings.Split(part, "/")
		if len(pieces) != 3 || pieces[2] != "" || (pieces[1] != "change" && pieces[1] != "delete") {
			http.NotFound(w, r)
			return
		}
		parsed, err := strconv.ParseInt(pieces[0], 10, 64)
		if err != nil || parsed <= 0 {
			http.NotFound(w, r)
			return
		}
		id, action = parsed, pieces[1]
	}
	if (action == "add" && !permissions.Add) || (action == "change" && !permissions.Change && !permissions.View) || (action == "delete" && !permissions.Delete) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := adminBookmarkAssetData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add bookmark asset", ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete}
	if action == "change" {
		data.Title = "Change bookmark asset"
		if !permissions.Change {
			data.Title = "View bookmark asset"
		}
	} else if action == "delete" {
		data.Title = "Delete bookmark asset"
	}
	if id != 0 {
		var size sql.NullInt64
		err := db.QueryRowContext(r.Context(), `SELECT bookmark_id,file,file_size,asset_type,content_type,display_name,status,gzip FROM bookmarks_bookmarkasset WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.BookmarkID, &data.File, &size, &data.AssetType, &data.ContentType, &data.DisplayName, &data.Status, &data.Gzip)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if size.Valid {
			data.FileSize = strconv.FormatInt(size.Int64, 10)
		}
		data.ObjectName = adminBookmarkAssetRepr(id, data.DisplayName)
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			http.Error(w, "CSRF verification failed", http.StatusForbidden)
			return
		}
		if action == "delete" {
			if r.PostForm.Get("post") != "yes" {
				http.Error(w, "Invalid form", http.StatusBadRequest)
				return
			}
			if err := deleteAdminBookmarkAsset(r, cfg, db, user.ID, id, data.File, adminBookmarkAssetRepr(id, data.DisplayName)); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, base, http.StatusFound)
			return
		}
		if action == "change" && !permissions.Change {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		previous := data
		data.BookmarkID, _ = strconv.ParseInt(r.PostForm.Get("bookmark"), 10, 64)
		data.File = strings.TrimSpace(r.PostForm.Get("file"))
		data.FileSize = strings.TrimSpace(r.PostForm.Get("file_size"))
		data.AssetType = strings.TrimSpace(r.PostForm.Get("asset_type"))
		data.ContentType = strings.TrimSpace(r.PostForm.Get("content_type"))
		data.DisplayName = strings.TrimSpace(r.PostForm.Get("display_name"))
		data.Status = strings.TrimSpace(r.PostForm.Get("status"))
		gzipValue := strings.ToLower(r.PostForm.Get("gzip"))
		data.Gzip = gzipValue != "" && gzipValue != "false" && gzipValue != "0"
		var size sql.NullInt64
		if data.FileSize == "" {
			data.Error = "File size is required."
		} else {
			parsed, err := strconv.ParseInt(data.FileSize, 10, 32)
			if err != nil {
				data.Error = "Enter a whole number for file size."
			} else {
				size = sql.NullInt64{Int64: parsed, Valid: true}
			}
		}
		if data.Error == "" && (len([]rune(data.File)) > 2048 || len([]rune(data.DisplayName)) > 2048 || len([]rune(data.AssetType)) > 64 || len([]rune(data.ContentType)) > 128 || len([]rune(data.Status)) > 64 || data.AssetType == "" || data.ContentType == "" || data.Status == "") {
			data.Error = "Enter valid asset type, content type, and status values."
		}
		if data.Error == "" {
			var exists bool
			if data.BookmarkID > 0 {
				if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM bookmarks_bookmark WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.BookmarkID).Scan(&exists); err != nil {
					http.Error(w, "Server error", 500)
					return
				}
			}
			if !exists {
				data.Error = "Select a valid bookmark."
			}
		}
		if data.Error == "" {
			var changedFields []string
			if action == "change" {
				for _, field := range []struct {
					label   string
					changed bool
				}{
					{"Bookmark", previous.BookmarkID != data.BookmarkID},
					{"File", previous.File != data.File},
					{"File size", previous.FileSize != data.FileSize},
					{"Asset type", previous.AssetType != data.AssetType},
					{"Content type", previous.ContentType != data.ContentType},
					{"Display name", previous.DisplayName != data.DisplayName},
					{"Status", previous.Status != data.Status},
					{"Gzip", previous.Gzip != data.Gzip},
				} {
					if field.changed {
						changedFields = append(changedFields, field.label)
					}
				}
			}
			if info, err := adminAssetFileInfo(cfg.DataDir, data.File); err == nil && info.Mode().IsRegular() {
				size = sql.NullInt64{Int64: info.Size(), Valid: true}
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			if action == "add" {
				query := `INSERT INTO bookmarks_bookmarkasset(date_created,bookmark_id,file,file_size,asset_type,content_type,display_name,status,gzip) VALUES (` + strings.Join([]string{assetMarker(cfg.DBEngine, 1), assetMarker(cfg.DBEngine, 2), assetMarker(cfg.DBEngine, 3), assetMarker(cfg.DBEngine, 4), assetMarker(cfg.DBEngine, 5), assetMarker(cfg.DBEngine, 6), assetMarker(cfg.DBEngine, 7), assetMarker(cfg.DBEngine, 8), assetMarker(cfg.DBEngine, 9)}, ",") + `) RETURNING id`
				err = tx.QueryRowContext(r.Context(), query, time.Now().UTC(), data.BookmarkID, data.File, size, data.AssetType, data.ContentType, data.DisplayName, data.Status, data.Gzip).Scan(&id)
			} else {
				query := `UPDATE bookmarks_bookmarkasset SET bookmark_id = ` + assetMarker(cfg.DBEngine, 1) + `, file = ` + assetMarker(cfg.DBEngine, 2) + `, file_size = ` + assetMarker(cfg.DBEngine, 3) + `, asset_type = ` + assetMarker(cfg.DBEngine, 4) + `, content_type = ` + assetMarker(cfg.DBEngine, 5) + `, display_name = ` + assetMarker(cfg.DBEngine, 6) + `, status = ` + assetMarker(cfg.DBEngine, 7) + `, gzip = ` + assetMarker(cfg.DBEngine, 8) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 9)
				_, err = tx.ExecContext(r.Context(), query, data.BookmarkID, data.File, size, data.AssetType, data.ContentType, data.DisplayName, data.Status, data.Gzip, id)
			}
			if err == nil {
				flag, message := 1, adminAdditionMessage
				if action == "change" {
					flag, message = 2, adminChangeMessage(changedFields)
				}
				err = writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "bookmarkasset", strconv.FormatInt(id, 10), adminBookmarkAssetRepr(id, data.DisplayName), flag, message)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			redirect := base
			if _, ok := r.PostForm["_addanother"]; ok {
				redirect = base + "add/"
			} else if _, ok := r.PostForm["_continue"]; ok {
				redirect = base + strconv.FormatInt(id, 10) + "/change/"
			}
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		var err error
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	var err error
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	rows, err := db.QueryContext(r.Context(), `SELECT id,title,url FROM bookmarks_bookmark ORDER BY date_added DESC, id DESC`)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	for rows.Next() {
		var option adminAssetBookmarkOption
		var title, bookmarkURL string
		if err := rows.Scan(&option.ID, &title, &bookmarkURL); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		if title == "" {
			title = bookmarkURL
		}
		urlRunes := []rune(bookmarkURL)
		if len(urlRunes) > 30 {
			urlRunes = urlRunes[:30]
		}
		option.Label = title + " (" + string(urlRunes) + "...)"
		option.Selected = option.ID == data.BookmarkID
		data.Bookmarks = append(data.Bookmarks, option)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(w, "Server error", 500)
		return
	}
	rows.Close()
	models, err := loadAdminModels(r, db, cfg, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	if data.ConfirmDelete {
		data.Deletion, err = adminSingleDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), "bookmarkasset", strconv.FormatInt(id, 10), data.ObjectName)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		localizeAdminDeletionGraph(data.Language, data.Deletion.Summary, data.Deletion.Nodes)
	}
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminBookmarkAssetTemplate.Execute(w, data)
	}
}

func adminAssetFileInfo(dataDir, name string) (os.FileInfo, error) {
	if name == "" {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(filepath.Join(dataDir, "assets"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Stat(name)
}

func adminBookmarkAssetRepr(id int64, displayName string) string {
	if displayName != "" {
		return displayName
	}
	return "Bookmark Asset #" + strconv.FormatInt(id, 10)
}

func deleteAdminBookmarkAsset(r *http.Request, cfg config.Config, db *sql.DB, actorID, id int64, name, repr string) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actorID, "bookmarks", "bookmarkasset", strconv.FormatInt(id, 10), repr, 3, ""); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks_bookmark SET latest_snapshot_id = NULL WHERE latest_snapshot_id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmarkasset WHERE id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
	return nil
}
