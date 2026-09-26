package httpserver

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_api_token.html admin_sidebar.html
var adminAPITokenFile embed.FS
var adminAPITokenTemplate = adminSidebarTemplate(adminAPITokenFile, "admin_api_token.html")

type adminAPITokenData struct {
	Language                                            string
	DashboardApps                                       []adminDashboardApp
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	Name, OwnerName, Error                              string
	ID, OwnerID                                         int64
	OriginalName                                        string
	OriginalOwnerID                                     int64
	ConfirmDelete, CanChange, CanDelete                 bool
	Users                                               []adminOwnerOption
	Deletion                                            adminSingleDeletion
}

func serveAdminAPIToken(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/apitoken/"
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
	data := adminAPITokenData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add api token", ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete}
	if action == "change" {
		data.Title = "Change api token"
		if !permissions.Change {
			data.Title = "View api token"
		}
	} else if action == "delete" {
		data.Title = "Delete api token"
	}
	if id != 0 {
		err := db.QueryRowContext(r.Context(), `SELECT t.name,t.user_id,u.username FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id WHERE t.id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.Name, &data.OwnerID, &data.OwnerName)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.OriginalName, data.OriginalOwnerID = data.Name, data.OwnerID
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
			repr := data.Name + " (" + data.OwnerName + ")"
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "apitoken", strconv.FormatInt(id, 10), repr, 3, ""); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_apitoken WHERE id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			http.Redirect(w, r, base, http.StatusFound)
			return
		}
		if action == "change" && !permissions.Change {
			http.Error(w, "Forbidden", 403)
			return
		}
		data.Name = strings.TrimSpace(r.PostForm.Get("name"))
		data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("user"), 10, 64)
		if data.Name == "" || len([]rune(data.Name)) > 128 || data.OwnerID <= 0 {
			data.Error = "Enter a name and user. The name must be at most 128 characters."
		} else {
			var exists bool
			if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.OwnerID).Scan(&exists); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if !exists {
				data.Error = "Select a valid user."
			}
		}
		if data.Error == "" {
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			var tokenID int64
			if action == "add" {
				var random [20]byte
				if _, err := rand.Read(random[:]); err != nil {
					http.Error(w, "Server error", 500)
					return
				}
				key := hex.EncodeToString(random[:])
				err = tx.QueryRowContext(r.Context(), `INSERT INTO bookmarks_apitoken (key,name,created,user_id) VALUES (`+assetMarker(cfg.DBEngine, 1)+`,`+assetMarker(cfg.DBEngine, 2)+`,`+assetMarker(cfg.DBEngine, 3)+`,`+assetMarker(cfg.DBEngine, 4)+`) RETURNING id`, key, data.Name, time.Now().UTC(), data.OwnerID).Scan(&tokenID)
				if err != nil {
					http.Error(w, "Server error", 500)
					return
				}
			} else {
				tokenID = id
				if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks_apitoken SET name = `+assetMarker(cfg.DBEngine, 1)+`, user_id = `+assetMarker(cfg.DBEngine, 2)+` WHERE id = `+assetMarker(cfg.DBEngine, 3), data.Name, data.OwnerID, id); err != nil {
					http.Error(w, "Server error", 500)
					return
				}
			}
			var ownerName string
			if err := tx.QueryRowContext(r.Context(), `SELECT username FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1), data.OwnerID).Scan(&ownerName); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			repr := data.Name + " (" + ownerName + ")"
			message, flag := adminAdditionMessage, 1
			if action == "change" {
				fields := make([]string, 0, 2)
				if data.Name != data.OriginalName {
					fields = append(fields, "Name")
				}
				if data.OwnerID != data.OriginalOwnerID {
					fields = append(fields, "User")
				}
				message, flag = adminChangeMessage(fields), 2
			}
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "apitoken", strconv.FormatInt(tokenID, 10), repr, flag, message); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			redirect := base
			if _, ok := r.PostForm["_addanother"]; ok {
				redirect = base + "add/"
			} else if _, ok := r.PostForm["_continue"]; ok {
				redirect = base + strconv.FormatInt(tokenID, 10) + "/change/"
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
		data.Users = append(data.Users, option)
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
		repr := data.Name + " (" + data.OwnerName + ")"
		data.Deletion, err = adminSingleDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), "apitoken", strconv.FormatInt(id, 10), repr)
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
		_ = adminAPITokenTemplate.Execute(w, data)
	}
}
