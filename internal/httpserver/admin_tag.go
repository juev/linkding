package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_tag.html
var adminTagFile embed.FS
var adminTagTemplate = template.Must(template.ParseFS(adminTagFile, "admin_tag.html"))

type adminTagData struct {
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	Name, DateAddedDate, DateAddedTime, Error           string
	ID, OwnerID                                         int64
	ConfirmDelete, CanChange, CanDelete                 bool
	Owners                                              []adminOwnerOption
}

func serveAdminTag(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	location, tzErr := adminTagLocation(cfg.TimeZone)
	if tzErr != nil {
		http.Error(w, "Server error", 500)
		return
	}
	base := cfg.URLPrefix() + "admin/bookmarks/tag/"
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
	data := adminTagData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add tag", ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete}
	if action == "change" {
		data.Title = "Change tag"
		if !permissions.Change {
			data.Title = "View tag"
		}
	} else if action == "delete" {
		data.Title = "Delete tag"
	}
	var previousAdded time.Time
	if id != 0 {
		var added time.Time
		var ownerName string
		err := db.QueryRowContext(r.Context(), `SELECT t.name,t.date_added,t.owner_id,u.username FROM bookmarks_tag AS t JOIN auth_user AS u ON u.id=t.owner_id WHERE t.id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.Name, &added, &data.OwnerID, &ownerName)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.DateAddedDate = added.In(location).Format("2006-01-02")
		data.DateAddedTime = added.In(location).Format("15:04:05")
		previousAdded = added
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
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "tag", strconv.FormatInt(id, 10), data.Name, 3, ""); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if _, err = tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmark_tags WHERE tag_id = `+assetMarker(cfg.DBEngine, 1), id); err == nil {
				_, err = tx.ExecContext(r.Context(), `DELETE FROM bookmarks_tag WHERE id = `+assetMarker(cfg.DBEngine, 1), id)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
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
		previousName, previousOwnerID := data.Name, data.OwnerID
		data.Name = r.PostForm.Get("name")
		data.Name = strings.TrimSpace(data.Name)
		data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("owner"), 10, 64)
		dateText, timeText := r.PostForm.Get("date_added_0"), r.PostForm.Get("date_added_1")
		var added time.Time
		if dateText == "" || timeText == "" {
			data.Error = "Enter a valid date and time."
		} else {
			var err error
			added, err = parseAdminTagDateTime(dateText, timeText, location)
			if err != nil {
				data.Error = "Enter a valid date and time."
			}
			data.DateAddedDate, data.DateAddedTime = dateText, timeText
		}
		if data.Error == "" && (data.Name == "" || len([]rune(data.Name)) > 64) {
			data.Error = "Enter a name and owner. The name must be at most 64 characters."
		}
		if data.Error == "" && data.OwnerID <= 0 {
			data.Error = "Select a valid owner."
		}
		if data.Error == "" {
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
				err = tx.QueryRowContext(r.Context(), `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (`+assetMarker(cfg.DBEngine, 1)+`,`+assetMarker(cfg.DBEngine, 2)+`,`+assetMarker(cfg.DBEngine, 3)+`) RETURNING id`, data.Name, added.UTC(), data.OwnerID).Scan(&id)
			} else {
				if previousName != data.Name {
					changedFields = append(changedFields, "Name")
				}
				if !previousAdded.Equal(added) {
					changedFields = append(changedFields, "Date added")
				}
				if previousOwnerID != data.OwnerID {
					changedFields = append(changedFields, "Owner")
				}
				_, err = tx.ExecContext(r.Context(), `UPDATE bookmarks_tag SET name = `+assetMarker(cfg.DBEngine, 1)+`, date_added = `+assetMarker(cfg.DBEngine, 2)+`, owner_id = `+assetMarker(cfg.DBEngine, 3)+` WHERE id = `+assetMarker(cfg.DBEngine, 4), data.Name, added.UTC(), data.OwnerID, id)
			}
			if err == nil {
				message, flag := adminAdditionMessage, 1
				if action == "change" {
					message, flag = adminChangeMessage(changedFields), 2
				}
				err = writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "tag", strconv.FormatInt(id, 10), data.Name, flag, message)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				http.Error(w, "Server error", 500)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminTagTemplate.Execute(w, data)
	}
}

func adminTagLocation(timeZone string) (*time.Location, error) {
	if timeZone == "" {
		timeZone = "UTC"
	}
	return time.LoadLocation(timeZone)
}

func parseAdminTagDateTime(dateText, timeText string, location *time.Location) (time.Time, error) {
	parsedDate, err := time.Parse("2006-01-02", dateText)
	if err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{"15:04:05", "15:04:05.999999", "15:04"} {
		parsedTime, err := time.Parse(layout, timeText)
		if err == nil {
			return time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), parsedTime.Hour(), parsedTime.Minute(), parsedTime.Second(), parsedTime.Nanosecond(), location), nil
		}
	}
	return time.Time{}, errors.New("invalid time")
}
