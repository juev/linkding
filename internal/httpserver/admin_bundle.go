package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_bundle.html admin_sidebar.html
var adminBundleFile embed.FS
var adminBundleTemplate = adminSidebarTemplate(adminBundleFile, "admin_bundle.html")

type adminBundleData struct {
	Language                                            string
	DashboardApps                                       []adminDashboardApp
	Prefix, Title, Username, CSRFToken, Action, ListURL string
	Name, Search, AnyTags, AllTags, ExcludedTags        string
	FilterUnread, FilterShared, Order, Error            string
	ID, OwnerID                                         int64
	ConfirmDelete, CanChange, CanDelete                 bool
	Owners                                              []adminOwnerOption
	Deletion                                            adminSingleDeletion
}

func serveAdminBundle(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/bookmarks/bookmarkbundle/"
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
	data := adminBundleData{Prefix: cfg.URLPrefix(), Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, Title: "Add bookmark bundle", ConfirmDelete: action == "delete", CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete, FilterUnread: "off", FilterShared: "off", Order: "0"}
	if action == "change" {
		data.Title = "Change bookmark bundle"
		if !permissions.Change {
			data.Title = "View bookmark bundle"
		}
	} else if action == "delete" {
		data.Title = "Delete bookmark bundle"
	}
	if id != 0 {
		var order int64
		err := db.QueryRowContext(r.Context(), `SELECT name,search,any_tags,all_tags,excluded_tags,filter_unread,filter_shared,"order",owner_id FROM bookmarks_bookmarkbundle WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.Name, &data.Search, &data.AnyTags, &data.AllTags, &data.ExcludedTags, &data.FilterUnread, &data.FilterShared, &order, &data.OwnerID)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Order = strconv.FormatInt(order, 10)
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			writeCSRFFailure(w, r)
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
			if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "bookmarkbundle", strconv.FormatInt(id, 10), data.Name, 3, ""); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks_bookmarkbundle WHERE id = `+assetMarker(cfg.DBEngine, 1), id); err != nil {
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
		previous := data
		data.Name = strings.TrimSpace(r.PostForm.Get("name"))
		data.Search = strings.TrimSpace(r.PostForm.Get("search"))
		data.AnyTags = strings.TrimSpace(r.PostForm.Get("any_tags"))
		data.AllTags = strings.TrimSpace(r.PostForm.Get("all_tags"))
		data.ExcludedTags = strings.TrimSpace(r.PostForm.Get("excluded_tags"))
		data.FilterUnread = r.PostForm.Get("filter_unread")
		data.FilterShared = r.PostForm.Get("filter_shared")
		data.Order = strings.TrimSpace(r.PostForm.Get("order"))
		data.OwnerID, _ = strconv.ParseInt(r.PostForm.Get("owner"), 10, 64)
		order, orderErr := strconv.ParseInt(data.Order, 10, 32)
		switch {
		case data.Name == "" || len([]rune(data.Name)) > 256:
			data.Error = "Enter a name of at most 256 characters."
		case len([]rune(data.Search)) > 256 || len([]rune(data.AnyTags)) > 1024 || len([]rune(data.AllTags)) > 1024 || len([]rune(data.ExcludedTags)) > 1024:
			data.Error = "Search must be at most 256 characters; tag filters must be at most 1024 characters."
		case !adminBundleFilterValid(data.FilterUnread) || !adminBundleFilterValid(data.FilterShared):
			data.Error = "Select a valid filter."
		case orderErr != nil:
			data.Error = "Enter a valid order."
		case data.OwnerID <= 0:
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
			now := time.Now().UTC()
			tx, err := db.BeginTx(r.Context(), nil)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			defer tx.Rollback()
			if action == "add" {
				err = tx.QueryRowContext(r.Context(), `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,filter_unread,filter_shared,"order",date_created,date_modified,owner_id) VALUES (`+adminBundleMarkers(cfg.DBEngine, 11)+`) RETURNING id`, data.Name, data.Search, data.AnyTags, data.AllTags, data.ExcludedTags, data.FilterUnread, data.FilterShared, order, now, now, data.OwnerID).Scan(&id)
			} else {
				_, err = tx.ExecContext(r.Context(), `UPDATE bookmarks_bookmarkbundle SET name = `+assetMarker(cfg.DBEngine, 1)+`,search = `+assetMarker(cfg.DBEngine, 2)+`,any_tags = `+assetMarker(cfg.DBEngine, 3)+`,all_tags = `+assetMarker(cfg.DBEngine, 4)+`,excluded_tags = `+assetMarker(cfg.DBEngine, 5)+`,filter_unread = `+assetMarker(cfg.DBEngine, 6)+`,filter_shared = `+assetMarker(cfg.DBEngine, 7)+`,"order" = `+assetMarker(cfg.DBEngine, 8)+`,date_modified = `+assetMarker(cfg.DBEngine, 9)+`,owner_id = `+assetMarker(cfg.DBEngine, 10)+` WHERE id = `+assetMarker(cfg.DBEngine, 11), data.Name, data.Search, data.AnyTags, data.AllTags, data.ExcludedTags, data.FilterUnread, data.FilterShared, order, now, data.OwnerID, id)
			}
			if err == nil {
				message, flag := adminAdditionMessage, 1
				if action == "change" {
					changed := make([]string, 0, 9)
					previousOrder, _ := strconv.ParseInt(previous.Order, 10, 32)
					if previous.Name != data.Name {
						changed = append(changed, "Name")
					}
					if previous.Search != data.Search {
						changed = append(changed, "Search")
					}
					if previous.AnyTags != data.AnyTags {
						changed = append(changed, "Any tags")
					}
					if previous.AllTags != data.AllTags {
						changed = append(changed, "All tags")
					}
					if previous.ExcludedTags != data.ExcludedTags {
						changed = append(changed, "Excluded tags")
					}
					if previous.FilterUnread != data.FilterUnread {
						changed = append(changed, "Filter unread")
					}
					if previous.FilterShared != data.FilterShared {
						changed = append(changed, "Filter shared")
					}
					if previousOrder != order {
						changed = append(changed, "Order")
					}
					if previous.OwnerID != data.OwnerID {
						changed = append(changed, "Owner")
					}
					message, flag = adminChangeMessage(changed), 2
				}
				err = writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "bookmarks", "bookmarkbundle", strconv.FormatInt(id, 10), data.Name, flag, message)
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
		data.Deletion, err = adminSingleDeletionGraph(r.Context(), db, cfg.DBEngine, cfg.URLPrefix(), "bookmarkbundle", strconv.FormatInt(id, 10), data.Name)
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
		_ = adminBundleTemplate.Execute(w, data)
	}
}

func adminBundleFilterValid(value string) bool {
	return value == "off" || value == "yes" || value == "no"
}

func adminBundleMarkers(engine string, count int) string {
	markers := make([]string, count)
	for i := range markers {
		markers[i] = assetMarker(engine, i+1)
	}
	return strings.Join(markers, ",")
}
