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

//go:embed admin_page.html admin_sidebar.html
var adminPageFile embed.FS
var adminPageTemplate = template.Must(template.New("admin_page.html").Funcs(template.FuncMap{
	"add":                func(a, b int) int { return a + b },
	"sub":                func(a, b int) int { return a - b },
	"lower":              strings.ToLower,
	"tr":                 adminTranslate,
	"appTitle":           adminAppTitle,
	"selectTitle":        adminSelectTitle,
	"addTitle":           adminAddTitle,
	"searchTitle":        adminSearchTitle,
	"actionCounter":      adminActionCounter,
	"deleteSelected":     adminDeleteSelected,
	"paginationTitle":    adminPaginationTitle,
	"rowActionAria":      adminRowActionAria,
	"filterTitle":        adminFilterTitle,
	"singleDeletePrompt": adminSingleDeletePrompt,
}).ParseFS(adminPageFile, "admin_page.html", "admin_sidebar.html"))

func adminSidebarTemplate(files embed.FS, name string) *template.Template {
	return template.Must(template.New(name).Funcs(template.FuncMap{
		"tr":                    adminTranslate,
		"appTitle":              adminAppTitle,
		"formTitle":             adminFormTitle,
		"relatedTitle":          adminRelatedTitle,
		"capTr":                 adminCapTranslate,
		"permissionLabel":       adminPermissionLabel,
		"historyTitle":          adminHistoryTitle,
		"passwordMinimum":       adminPasswordMinimum,
		"passwordPrompt":        adminPasswordPrompt,
		"passwordEnableMessage": adminPasswordEnableMessage,
		"singleDeletePrompt":    adminSingleDeletePrompt,
		"bulkDeletePrompt":      adminBulkDeletePrompt,
		"deletionLabel":         adminDeletionLabel,
	}).ParseFS(files, name, "admin_sidebar.html"))
}

type adminTask struct {
	ID      int64
	Name    string
	Args    string
	Retries int
}

type adminPageData struct {
	Language, Direction                                        string
	Prefix, Title, Username, ModelName, ModelPath, AddURL      string
	SearchQuery, UserFilter, AllUsersURL                       string
	UserFilterParam, UserFilterTitle, AddLabel                 string
	PreviousPageURL, NextPageURL                               string
	ShowCountsURL, ShowCountsLabel                             string
	ClearFiltersURL                                            string
	CSRFToken, ActionMessage                                   string
	Tasks                                                      []adminTask
	Models                                                     []adminModelLink
	DashboardApps                                              []adminDashboardApp
	RecentActions                                              []adminRecentAction
	UserFilters                                                []adminUserFilter
	ListFilters                                                []adminFilterGroup
	ModelColumns                                               []string
	ModelHeaders                                               []adminListHeader
	ModelRows                                                  []adminListRow
	TaskCount                                                  int64
	Page, Pages                                                int
	IsTaskList, IsModelList, IsAppIndex, IsSearchableList      bool
	AppSlug                                                    string
	AppLabel, AppPath, ModelSlug, PluralLabel                  string
	IsTagList, IsBookmarkList, IsUserList, IsDefaultActionList bool
	CanAdd, CanDelete, IsEditableModel                         bool
	ShowFacets, HasActiveFilters                               bool
}

func serveAdmin(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository) {
	root := cfg.URLPrefix() + "admin/"
	w.Header().Set("Vary", "Cookie, Accept-Language")
	w.Header().Set("Content-Language", selectedAdminLanguage(r).Code)
	if !strings.HasPrefix(r.URL.Path, root) {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == root+"logout/" {
		serveLogout(w, r, r.URL.Path, cfg, users)
		return
	}
	if r.URL.Path == root+"login/" {
		serveAdminLogin(w, r, cfg, users)
		return
	}
	var user auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		user, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if user.ID == 0 || !user.IsActive || !user.IsStaff {
		next := strings.ReplaceAll(url.QueryEscape(r.URL.RequestURI()), "%2F", "/")
		http.Redirect(w, r, root+"login/?next="+next, http.StatusFound)
		return
	}
	if r.URL.Path == root+"password_change/" {
		serveChangePassword(w, r, r.URL.Path, cfg, db, users)
		return
	}
	if r.URL.Path == root+"password_change/done/" {
		servePasswordChangeDone(w, r, r.URL.Path, cfg, db, users)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"auth/user/") && strings.HasSuffix(r.URL.Path, "/history/") {
		serveAdminUserHistory(w, r, cfg, db, user)
		return
	}
	if strings.HasPrefix(r.URL.Path, root+"bookmarks/") && strings.HasSuffix(r.URL.Path, "/history/") {
		pieces := strings.Split(strings.TrimPrefix(r.URL.Path, root+"bookmarks/"), "/")
		if len(pieces) == 4 && pieces[2] == "history" && pieces[3] == "" {
			switch pieces[0] {
			case "toast", "apitoken", "feedtoken", "bookmarkbundle", "bookmarkasset":
				serveAdminOtherHistory(w, r, cfg, db, user, pieces[0], pieces[1])
				return
			}
		}
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
	if strings.HasPrefix(r.URL.Path, root+"auth/user/") && r.URL.Path != root+"auth/user/" {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "auth", "user")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminUser(w, r, cfg, db, users, user, permissions)
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
	if r.URL.Path == root+"auth/user/" && r.Method == http.MethodPost {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, "auth", "user")
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		serveAdminUserAction(w, r, cfg, db, user, permissions)
		return
	}
	if r.Method == http.MethodPost {
		for _, model := range []string{"bookmarkasset", "bookmarkbundle", "toast", "apitoken", "feedtoken"} {
			if r.URL.Path != root+"bookmarks/"+model+"/" {
				continue
			}
			definition, _ := findAdminModel("bookmarks", model)
			permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, definition.App, definition.Model)
			if err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			serveAdminDefaultAction(w, r, cfg, db, user, definition, permissions)
			return
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	language := selectedAdminLanguage(r)
	data := adminPageData{Prefix: cfg.URLPrefix(), Title: adminTranslate(language.Code, "Site administration"), Language: language.Code, Direction: language.Dir, Username: user.Username, Page: 1}
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
	appIndex := ""
	if r.URL.Path == root+"auth/" || r.URL.Path == root+"bookmarks/" {
		appIndex = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, root), "/")
	}
	if r.URL.Path != root && r.URL.Path != root+"tasks/" && appIndex == "" {
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
		if appIndex != "" {
			data.IsAppIndex, data.AppSlug = true, appIndex
			for _, model := range models {
				if model.App == appIndex {
					data.Models = append(data.Models, model)
				}
			}
			label := "Bookmarks"
			if appIndex == "auth" {
				label = "Authentication and Authorization"
			}
			data.Title = adminAppIndexTitle(language.Code, adminTranslate(language.Code, label))
		} else {
			data.Models = models
		}
		data.DashboardApps = groupAdminDashboardApps(cfg, data.Models)
		if !data.IsAppIndex {
			data.RecentActions, err = loadAdminRecentActions(r.Context(), db, cfg, user.ID)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			for index := range data.RecentActions {
				data.RecentActions[index].Verb = adminTranslate(language.Code, data.RecentActions[index].Verb)
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method == http.MethodHead {
		return
	}
	_ = adminPageTemplate.Execute(w, data)
}
