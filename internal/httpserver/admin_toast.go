package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_toast.html admin_sidebar.html
var adminToastFile embed.FS
var adminToastTemplate = adminSidebarTemplate(adminToastFile, "admin_toast.html")

type adminOwnerOption struct {
	ID       int64
	Username string
	Selected bool
}

type adminToastData struct {
	Language                                            string
	DashboardApps                                       []adminDashboardApp
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	Key, Message, ObjectName, Error                     string
	ID, OwnerID                                         int64
	Acknowledged, ConfirmDelete, CanChange, CanDelete   bool
	Owners                                              []adminOwnerOption
	Deletion                                            adminSingleDeletion
}

func serveAdminToast(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/toast/"
	part := strings.TrimPrefix(r.URL.Path, base)
	var action string
	var id int64
	if part == "add/" {
		action = "add"
	} else {
		pieces := strings.Split(part, "/")
		if len(pieces) != 3 || pieces[2] != "" || (pieces[1] != "change" && pieces[1] != "delete") {
			writeNotFound(w, r)
			return
		}
		parsed, err := strconv.ParseInt(pieces[0], 10, 64)
		if err != nil || parsed <= 0 {
			writeNotFound(w, r)
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
	data := adminToastData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add toast", ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete}
	if action == "change" {
		data.Title = "Change toast"
		if !permissions.Change {
			data.Title = "View toast"
		}
	} else if action == "delete" {
		data.Title = "Delete toast"
	}
	if id != 0 {
		err := db.QueryRowContext(r.Context(), `SELECT key,message,acknowledged,owner_id FROM bookmarks_toast WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.Key, &data.Message, &data.Acknowledged, &data.OwnerID)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.ObjectName = "Toast object (" + strconv.FormatInt(id, 10) + ")"
	}
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
		if action == "delete" {
			if r.PostForm.Get("post") != "yes" {
				http.Error(w, "Invalid form", 400)
				return
			}
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "toast", strconv.FormatInt(id, 10), "Toast object ("+strconv.FormatInt(id, 10)+")", 3, ""); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_toast WHERE id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			writeRedirect(w, r, base)
			return
		}
		if action == "change" && !permissions.Change {
			http.Error(w, "Forbidden", 403)
			return
		}
		previousKey, previousMessage, previousAcknowledged, previousOwnerID := data.Key, data.Message, data.Acknowledged, data.OwnerID
		data.Key = strings.TrimSpace(r.PostForm.Get("key"))
		data.Message = strings.TrimSpace(r.PostForm.Get("message"))
		data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("owner"), 10, 64)
		data.Acknowledged = r.PostForm.Has("acknowledged")
		if data.Key == "" || data.Message == "" || len([]rune(data.Key)) > 50 || data.OwnerID <= 0 {
			data.Error = "Enter a key, message, and owner. The key must be at most 50 characters."
		} else {
			var exists bool
			if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.OwnerID).Scan(&exists); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if !exists {
				data.Error = "Select a valid owner."
			}
		}
		if data.Error == "" {
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			var changedFields []string
			if action == "add" {
				err = tx.QueryRowContext(r.Context(), `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES (`+assetMarker(cfg.DBEngine, 1)+`,`+assetMarker(cfg.DBEngine, 2)+`,`+assetMarker(cfg.DBEngine, 3)+`,`+assetMarker(cfg.DBEngine, 4)+`) RETURNING id`, data.Key, data.Message, data.Acknowledged, data.OwnerID).Scan(&id)
			} else {
				if previousKey != data.Key {
					changedFields = append(changedFields, "Key")
				}
				if previousMessage != data.Message {
					changedFields = append(changedFields, "Message")
				}
				if previousAcknowledged != data.Acknowledged {
					changedFields = append(changedFields, "Acknowledged")
				}
				if previousOwnerID != data.OwnerID {
					changedFields = append(changedFields, "Owner")
				}
				_, err = tx.ExecContext(r.Context(), `UPDATE bookmarks_toast SET key = `+assetMarker(cfg.DBEngine, 1)+`, message = `+assetMarker(cfg.DBEngine, 2)+`, acknowledged = `+assetMarker(cfg.DBEngine, 3)+`, owner_id = `+assetMarker(cfg.DBEngine, 4)+` WHERE id = `+assetMarker(cfg.DBEngine, 5), data.Key, data.Message, data.Acknowledged, data.OwnerID, id)
			}
			if err == nil {
				repr := "Toast object (" + strconv.FormatInt(id, 10) + ")"
				message, flag := adminAdditionMessage, 1
				if action == "change" {
					message, flag = adminChangeMessage(changedFields), 2
				}
				err = writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "toast", strconv.FormatInt(id, 10), repr, flag, message)
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
			writeRedirect(w, r, redirect)
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
	rows, err := db.QueryContext(r.Context(), `SELECT id,username FROM auth_user ORDER BY username`)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	for rows.Next() {
		var option adminOwnerOption
		if err := rows.Scan(&option.ID, &option.Username); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		option.Selected = option.ID == data.OwnerID
		data.Owners = append(data.Owners, option)
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
		data.Deletion, err = adminSingleDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), "toast", strconv.FormatInt(id, 10), data.ObjectName)
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
		_ = adminToastTemplate.Execute(w, data)
	}
}
