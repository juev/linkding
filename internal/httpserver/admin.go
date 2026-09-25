package httpserver

import (
	"database/sql"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed admin_page.html
var adminPageFile embed.FS
var adminPageTemplate = template.Must(template.New("admin_page.html").Funcs(template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
}).ParseFS(adminPageFile, "admin_page.html"))

type adminTask struct {
	ID      int64
	Name    string
	Args    string
	Retries int
}

type adminPageData struct {
	Prefix, Title, Username, ModelName, ModelPath                 string
	SearchQuery, UserFilter, AllUsersURL                          string
	UserFilterParam, UserFilterTitle, AddLabel                    string
	PreviousPageURL, NextPageURL                                  string
	CSRFToken, ActionMessage                                      string
	Tasks                                                         []adminTask
	Models                                                        []adminModelLink
	UserFilters                                                   []adminUserFilter
	ListFilters                                                   []adminFilterGroup
	ModelColumns                                                  []string
	ModelRows                                                     []adminListRow
	TaskCount                                                     int64
	Page, Pages                                                   int
	IsTaskList, IsModelList, IsSearchableList                     bool
	IsTagList, IsBookmarkList, CanAdd, CanDelete, IsEditableModel bool
}

func serveAdmin(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository) {
	root := cfg.URLPrefix() + "admin/"
	if !strings.HasPrefix(r.URL.Path, root) {
		http.NotFound(w, r)
		return
	}
	var user auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		user, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if user.ID == 0 {
		http.Redirect(w, r, cfg.URLPrefix()+"login/?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return
	}
	if !user.IsActive || !user.IsStaff {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/toast/") && r.URL.Path != root+"bookmarks/toast/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "toast")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminToast(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/apitoken/") && r.URL.Path != root+"bookmarks/apitoken/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "apitoken")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminAPIToken(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/feedtoken/") && r.URL.Path != root+"bookmarks/feedtoken/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "feedtoken")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminFeedToken(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/tag/") && r.URL.Path != root+"bookmarks/tag/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "tag")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminTag(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/bookmarkbundle/") && r.URL.Path != root+"bookmarks/bookmarkbundle/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "bookmarkbundle")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminBundle(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/bookmarkasset/") && r.URL.Path != root+"bookmarks/bookmarkasset/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "bookmarkasset")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminBookmarkAsset(w, r, cfg, db, user, permissions)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/bookmark/") && r.URL.Path != root+"bookmarks/bookmark/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "bookmark")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminBookmark(w, r, cfg, db, user, permissions)
		return
	}
	if r.URL.Path == root+"bookmarks/tag/" && r.Method == http.MethodPost {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "tag")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminTagAction(w, r, cfg, db, user, permissions)
		return
	}
	if r.URL.Path == root+"bookmarks/bookmark/" && r.Method == http.MethodPost {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "bookmarks", "bookmark")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminBookmarkAction(w, r, cfg, db, permissions)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := adminPageData{Prefix: cfg.URLPrefix(), Title: "Site administration", Username: user.Username, Page: 1}
	if r.URL.Path != root && r.URL.Path != root+"tasks/" {
		relative := strings.TrimPrefix(r.URL.Path, root)
		parts := strings.Split(relative, "/")
		if len(parts) != 3 || parts[2] != "" {
			http.NotFound(w, r)
			return
		}
		definition, found := findAdminModel(parts[0], parts[1])
		if !found {
			http.NotFound(w, r)
			return
		}
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, definition.App, definition.Model)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminModelList(w, r, cfg, db, user, definition, permissions, data)
		return
	}
	if r.URL.Path == root+"tasks/" {
		data.Title = "Background tasks"
		data.IsTaskList = true
		if err := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM linkding_job WHERE status = 'pending'`).Scan(&data.TaskCount); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Pages = max(1, int((data.TaskCount+99)/100))
		if requested, err := strconv.Atoi(r.URL.Query().Get("p")); err == nil && requested > 0 {
			data.Page = min(requested, data.Pages)
		}
		query := `SELECT id,kind,payload,attempts FROM linkding_job WHERE status = 'pending' ORDER BY id LIMIT 100 OFFSET ` + assetMarker(cfg.DBEngine, 1)
		rows, err := db.QueryContext(r.Context(), query, (data.Page-1)*100)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		for rows.Next() {
			var task adminTask
			if err := rows.Scan(&task.ID, &task.Name, &task.Args, &task.Retries); err != nil {
				rows.Close()
				http.Error(w, "Server error", 500)
				return
			}
			data.Tasks = append(data.Tasks, task)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			http.Error(w, "Server error", 500)
			return
		}
		rows.Close()
	} else {
		models, err := loadAdminModels(r, db, cfg, user)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Models = models
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	_ = adminPageTemplate.Execute(w, data)
}
