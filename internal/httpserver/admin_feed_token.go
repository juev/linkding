package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_feed_token.html
var adminFeedTokenFile embed.FS

var adminFeedTokenTemplate = template.Must(template.ParseFS(adminFeedTokenFile, "admin_feed_token.html"))

type adminFeedTokenData struct {
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	Key, KeyPath, OwnerName, Error                      string
	OwnerID                                             int64
	OriginalOwnerID                                     int64
	ConfirmDelete, CanChange, CanDelete                 bool
	Users                                               []adminOwnerOption
}

func serveAdminFeedToken(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/feedtoken/"
	part := strings.TrimPrefix(r.URL.EscapedPath(), base)
	var action, key string
	if part == "add/" {
		action = "add"
	} else {
		for _, suffix := range []string{"/change/", "/delete/"} {
			if strings.HasSuffix(part, suffix) {
				decoded, err := url.PathUnescape(strings.TrimSuffix(part, suffix))
				if err == nil && decoded != "" {
					key, action = decoded, strings.Trim(suffix, "/")
				}
				break
			}
		}
		if key == "" {
			http.NotFound(w, r)
			return
		}
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

	escapedKey := url.PathEscape(key)
	actionURL := r.URL.EscapedPath()
	data := adminFeedTokenData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: actionURL, ListURL: base, Key: key, KeyPath: escapedKey, ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete}
	switch action {
	case "add":
		data.Title = "Add Feed token"
	case "change":
		data.Title = "Change Feed token"
		if !permissions.Change {
			data.Title = "View Feed token"
		}
	case "delete":
		data.Title = "Delete Feed token"
	}
	if action != "add" {
		if err := db.QueryRowContext(r.Context(), `SELECT t.user_id,u.username FROM bookmarks_feedtoken AS t JOIN auth_user AS u ON u.id=t.user_id WHERE t.key = `+assetMarker(cfg.DBEngine, 1), key).Scan(&data.OwnerID, &data.OwnerName); errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		data.OriginalOwnerID = data.OwnerID
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
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			defer tx.Rollback()
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "feedtoken", key, key, 3, ""); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_feedtoken WHERE key = `+assetMarker(cfg.DBEngine, 1), key); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			if err := tx.Commit(); err != nil {
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
		originalKey := key
		data.Key = strings.TrimSpace(r.PostForm.Get("key"))
		keyChanged := data.Key != originalKey
		data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("user"), 10, 64)
		if data.Key == "" || len([]rune(data.Key)) > 40 || data.OwnerID <= 0 {
			data.Error = "Enter a key of at most 40 characters and select a user."
		} else {
			var exists bool
			if err := db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1)+`)`, data.OwnerID).Scan(&exists); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			if !exists {
				data.Error = "Select a valid user."
			} else {
				query := `SELECT EXISTS(SELECT 1 FROM bookmarks_feedtoken WHERE key = ` + assetMarker(cfg.DBEngine, 1)
				args := []any{data.Key}
				if action == "change" {
					query += ` AND key <> ` + assetMarker(cfg.DBEngine, 2)
					args = append(args, originalKey)
				}
				query += `)`
				if err := db.QueryRowContext(r.Context(), query, args...).Scan(&exists); err != nil {
					http.Error(w, "Server error", http.StatusInternalServerError)
					return
				}
				if exists {
					data.Error = "A feed token with this key already exists."
				} else {
					query = `SELECT EXISTS(SELECT 1 FROM bookmarks_feedtoken WHERE user_id = ` + assetMarker(cfg.DBEngine, 1)
					args = []any{data.OwnerID}
					if action == "change" && !keyChanged {
						query += ` AND key <> ` + assetMarker(cfg.DBEngine, 2)
						args = append(args, originalKey)
					}
					query += `)`
					if err := db.QueryRowContext(r.Context(), query, args...).Scan(&exists); err != nil {
						http.Error(w, "Server error", http.StatusInternalServerError)
						return
					}
					if exists {
						data.Error = "This user already has a feed token."
					}
				}
			}
		}
		if data.Error == "" {
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			defer tx.Rollback()
			if action == "add" {
				_, err := tx.ExecContext(r.Context(), `INSERT INTO bookmarks_feedtoken (key,created,user_id) VALUES (`+assetMarker(cfg.DBEngine, 1)+`,`+assetMarker(cfg.DBEngine, 2)+`,`+assetMarker(cfg.DBEngine, 3)+`)`, data.Key, time.Now().UTC(), data.OwnerID)
				if err != nil {
					http.Error(w, "Server error", http.StatusInternalServerError)
					return
				}
			} else if keyChanged {
				if _, err := tx.ExecContext(r.Context(), `INSERT INTO bookmarks_feedtoken (key,created,user_id) VALUES (`+assetMarker(cfg.DBEngine, 1)+`,`+assetMarker(cfg.DBEngine, 2)+`,`+assetMarker(cfg.DBEngine, 3)+`)`, data.Key, time.Now().UTC(), data.OwnerID); err != nil {
					http.Error(w, "Server error", http.StatusInternalServerError)
					return
				}
			} else {
				if _, err := tx.ExecContext(r.Context(), `UPDATE bookmarks_feedtoken SET user_id = `+assetMarker(cfg.DBEngine, 1)+` WHERE key = `+assetMarker(cfg.DBEngine, 2), data.OwnerID, originalKey); err != nil {
					http.Error(w, "Server error", http.StatusInternalServerError)
					return
				}
			}
			message, flag := adminAdditionMessage, 1
			objectID, repr := data.Key, data.Key
			if action == "change" {
				fields := make([]string, 0, 2)
				if keyChanged {
					fields = append(fields, "Key")
				}
				if data.OwnerID != data.OriginalOwnerID {
					fields = append(fields, "User")
				}
				message, flag = adminChangeMessage(fields), 2
			}
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "feedtoken", objectID, repr, flag, message); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, base, http.StatusFound)
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
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	var err error
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	rows, err := db.QueryContext(r.Context(), `SELECT id,username FROM auth_user ORDER BY username`)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var option adminOwnerOption
		if err := rows.Scan(&option.ID, &option.Username); err != nil {
			rows.Close()
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
		option.Selected = option.ID == data.OwnerID
		data.Users = append(data.Users, option)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	rows.Close()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminFeedTokenTemplate.Execute(w, data)
	}
}
