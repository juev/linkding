package httpserver

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

type adminModelDefinition struct {
	App, Model, Label, Plural, Query string
	Columns                          []string
}

var adminModels = []adminModelDefinition{
	{App: "auth", Model: "user", Label: "User", Plural: "Users", Query: `SELECT id,username,email,is_staff,is_active FROM auth_user ORDER BY id DESC`, Columns: []string{"Username", "Email address", "Staff status", "Active"}},
	{App: "bookmarks", Model: "bookmark", Label: "Bookmark", Plural: "Bookmarks", Query: `SELECT b.id,COALESCE(NULLIF(b.title,''),b.url),b.url,b.is_archived,u.username,b.date_added FROM bookmarks_bookmark AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.date_added DESC,b.id DESC`, Columns: []string{"Title", "URL", "Is archived", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkasset", Label: "Bookmark asset", Plural: "Bookmark assets", Query: `SELECT id,display_name,date_created,status FROM bookmarks_bookmarkasset ORDER BY id DESC`, Columns: []string{"Display name", "Date created", "Status"}},
	{App: "bookmarks", Model: "tag", Label: "Tag", Plural: "Tags", Query: `SELECT t.id,t.name,(SELECT COUNT(*) FROM bookmarks_bookmark_tags AS bt WHERE bt.tag_id=t.id),u.username,t.date_added FROM bookmarks_tag AS t JOIN auth_user AS u ON u.id=t.owner_id ORDER BY t.date_added DESC,t.id DESC`, Columns: []string{"Name", "Bookmarks count", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkbundle", Label: "Bookmark bundle", Plural: "Bookmark bundles", Query: `SELECT b.id,b.name,u.username,b."order",b.search,b.date_created FROM bookmarks_bookmarkbundle AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.id DESC`, Columns: []string{"Name", "Owner", "Order", "Search", "Date created"}},
	{App: "bookmarks", Model: "apitoken", Label: "API token", Plural: "API tokens", Query: `SELECT t.id,t.name,u.username,t.created FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id ORDER BY t.created DESC,t.id DESC`, Columns: []string{"Name", "User", "Created"}},
	{App: "bookmarks", Model: "toast", Label: "Toast", Plural: "Toasts", Query: `SELECT t.id,t.key,t.message,u.username,t.acknowledged FROM bookmarks_toast AS t JOIN auth_user AS u ON u.id=t.owner_id ORDER BY t.id DESC`, Columns: []string{"Key", "Message", "Owner", "Acknowledged"}},
	{App: "bookmarks", Model: "feedtoken", Label: "Feed token", Plural: "Feed tokens", Query: `SELECT t.key,t.key,u.username FROM bookmarks_feedtoken AS t JOIN auth_user AS u ON u.id=t.user_id ORDER BY t.created DESC`, Columns: []string{"Key", "User"}},
}

type adminModelLink struct {
	Path, AddPath, Label, AppLabel string
}

type adminListRow struct {
	ID    string
	Cells []string
	Link  string
}

func findAdminModel(app, model string) (adminModelDefinition, bool) {
	for _, definition := range adminModels {
		if definition.App == app && definition.Model == model {
			return definition, true
		}
	}
	return adminModelDefinition{}, false
}

func loadAdminModels(r *http.Request, db *sql.DB, cfg config.Config, user auth.User) ([]adminModelLink, error) {
	var links []adminModelLink
	for _, definition := range adminModels {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, definition.App, definition.Model)
		if err != nil {
			return nil, err
		}
		if !permissions.canList() && !(definition.Model == "toast" && permissions.Add) {
			continue
		}
		appLabel := "Bookmarks"
		if definition.App == "auth" {
			appLabel = "Authentication and Authorization"
		}
		link := adminModelLink{Label: definition.Plural, AppLabel: appLabel}
		base := cfg.URLPrefix() + "admin/" + definition.App + "/" + definition.Model + "/"
		if permissions.canList() {
			link.Path = base
		}
		if definition.Model == "toast" && permissions.Add {
			link.AddPath = base + "add/"
		}
		links = append(links, link)
	}
	return links, nil
}

func serveAdminModelList(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, definition adminModelDefinition, permissions adminPermissions, data adminPageData) {
	if !permissions.canList() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	page := 1
	if requested, err := strconv.Atoi(r.URL.Query().Get("p")); err == nil && requested > 0 {
		page = requested
	}
	var total int64
	if err := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM (`+definition.Query+`) AS records`).Scan(&total); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	pages := max(1, int((total+99)/100))
	page = min(page, pages)
	query := definition.Query + ` LIMIT 100 OFFSET ` + assetMarker(cfg.DBEngine, 1)
	rows, err := db.QueryContext(r.Context(), query, (page-1)*100)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	defer rows.Close()
	data.Title = "Select " + definition.Label + " to change"
	data.IsModelList = true
	data.ModelName = definition.Plural
	data.ModelColumns = definition.Columns
	data.TaskCount = total
	data.Page, data.Pages = page, pages
	data.CanAdd = permissions.Add
	data.ModelPath = cfg.URLPrefix() + "admin/" + definition.App + "/" + definition.Model + "/"
	for rows.Next() {
		values := make([]any, len(definition.Columns)+1)
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		row := adminListRow{ID: adminValueString(values[0])}
		if definition.Model == "toast" && (permissions.View || permissions.Change) {
			row.Link = data.ModelPath + row.ID + "/change/"
		}
		for _, value := range values[1:] {
			row.Cells = append(row.Cells, adminValueString(value))
		}
		data.ModelRows = append(data.ModelRows, row)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminPageTemplate.Execute(w, data)
	}
}

func adminValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format("Jan 2, 2006, 3:04 p.m.")
	case bool:
		if typed {
			return "Yes"
		}
		return "No"
	default:
		return fmt.Sprint(value)
	}
}
