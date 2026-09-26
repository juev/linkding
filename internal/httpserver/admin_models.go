package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

type adminModelDefinition struct {
	App, Model, Label, Plural, Query string
	Columns                          []string
}

var adminModels = []adminModelDefinition{
	{App: "auth", Model: "user", Label: "User", Plural: "Users", Query: `SELECT id,username,email,first_name,last_name,is_staff FROM auth_user ORDER BY username`, Columns: []string{"Username", "Email address", "First name", "Last name", "Staff status"}},
	{App: "bookmarks", Model: "bookmark", Label: "Bookmark", Plural: "Bookmarks", Query: `SELECT b.id,COALESCE(NULLIF(b.title,''),b.url),b.url,b.is_archived,u.username,b.date_added FROM bookmarks_bookmark AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.date_added DESC,b.id DESC`, Columns: []string{"Resolved title", "Url", "Is archived", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkasset", Label: "Bookmark asset", Plural: "Bookmark assets", Query: `SELECT id,COALESCE(NULLIF(display_name,''),'Bookmark Asset #' || id),date_created,status FROM bookmarks_bookmarkasset ORDER BY id DESC`, Columns: []string{"Display Name", "Date created", "Status"}},
	{App: "bookmarks", Model: "tag", Label: "Tag", Plural: "Tags", Query: `SELECT t.id,t.name,(SELECT COUNT(*) FROM bookmarks_bookmark_tags AS bt WHERE bt.tag_id=t.id),u.username,t.date_added FROM bookmarks_tag AS t JOIN auth_user AS u ON u.id=t.owner_id ORDER BY t.date_added DESC,t.id DESC`, Columns: []string{"Name", "Bookmarks count", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkbundle", Label: "Bookmark bundle", Plural: "Bookmark bundles", Query: `SELECT b.id,b.name,u.username,b."order",b.search,b.any_tags,b.all_tags,b.excluded_tags,b.filter_shared,b.filter_unread,b.date_created FROM bookmarks_bookmarkbundle AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.id DESC`, Columns: []string{"Name", "Owner", "Order", "Search", "Any tags", "All tags", "Excluded tags", "Filter shared", "Filter unread", "Date created"}},
	{App: "bookmarks", Model: "apitoken", Label: "API token", Plural: "API tokens", Query: `SELECT t.id,t.name,u.username,t.created FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id ORDER BY t.created DESC,t.id DESC`, Columns: []string{"Name", "User", "Created"}},
	{App: "bookmarks", Model: "toast", Label: "Toast", Plural: "Toasts", Query: `SELECT t.id,t.key,t.message,u.username,t.acknowledged FROM bookmarks_toast AS t JOIN auth_user AS u ON u.id=t.owner_id ORDER BY t.id DESC`, Columns: []string{"Key", "Message", "Owner", "Acknowledged"}},
	{App: "bookmarks", Model: "feedtoken", Label: "Feed token", Plural: "Feed tokens", Query: `SELECT t.key,t.key,u.username FROM bookmarks_feedtoken AS t JOIN auth_user AS u ON u.id=t.user_id ORDER BY t.key DESC`, Columns: []string{"Key", "User"}},
}

type adminModelLink struct {
	Path, AddPath, Label, AppLabel, App, Model, ChangeLabel string
}

type adminListRow struct {
	ID, Link, ActionLabel string
	Cells                 []string
}

type adminListHeader struct {
	Label, URL, ToggleURL, RemoveURL string
	Sortable, Sorted, Ascending      bool
}

type adminUserFilter struct {
	Username, URL string
	Selected      bool
	Count         int64
}

type adminFilterGroup struct {
	Title, Param, Value, AllURL string
	ExtraParam, ExtraValue      string
	IsNull                      bool
	Options                     []adminUserFilter
}

var adminSearchSplit = regexp.MustCompile(`((?:[^\s'"]*(?:(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')[^\s'"]*)+)|\S+)`)

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
	language := selectedAdminLanguage(r).Code
	for _, definition := range adminModels {
		permissions, err := loadAdminPermissions(r.Context(), db, cfg.DBEngine, user, definition.App, definition.Model)
		if err != nil {
			return nil, err
		}
		canAdd := adminEditableModel(definition.Model) && permissions.Add
		if !permissions.canList() && !canAdd {
			continue
		}
		appLabel := "Bookmarks"
		if definition.App == "auth" {
			appLabel = adminTranslate(language, "Authentication and Authorization")
		}
		label := definition.Plural
		if definition.Model == "apitoken" {
			label = "Api tokens"
		} else if definition.Model == "user" {
			label = adminCapitalized(adminTranslate(language, "users"))
		}
		link := adminModelLink{Label: label, AppLabel: appLabel, App: definition.App, Model: definition.Model}
		base := cfg.URLPrefix() + "admin/" + definition.App + "/" + definition.Model + "/"
		if permissions.canList() {
			link.Path = base
			link.ChangeLabel = "Change"
			if !permissions.Change {
				link.ChangeLabel = "View"
			}
		}
		if canAdd {
			link.AddPath = base + "add/"
		}
		links = append(links, link)
	}
	sort.SliceStable(links, func(i, j int) bool {
		if links[i].AppLabel != links[j].AppLabel {
			return links[i].AppLabel < links[j].AppLabel
		}
		return strings.ToLower(links[i].Label) < strings.ToLower(links[j].Label)
	})
	return links, nil
}

func serveAdminModelList(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, definition adminModelDefinition, permissions adminPermissions, data adminPageData) {
	if !permissions.canList() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	location := time.UTC
	if cfg.TimeZone != "" {
		var err error
		location, err = time.LoadLocation(cfg.TimeZone)
		if err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
	}
	page := 1
	if requested, err := strconv.Atoi(r.URL.Query().Get("p")); err == nil && requested > 0 {
		page = requested
	}
	baseQuery := definition.Query
	var args []any
	if definition.App == "auth" && definition.Model == "user" {
		data.IsSearchableList = true
		data.SearchQuery = r.URL.Query().Get("q")
		data.ListFilters = adminUserListFilters(r.URL.Query())
		baseQuery, args = adminUserListQuery(cfg.DBEngine, data.SearchQuery, r.URL.Query())
		if err := populateAdminUserListFilters(r.Context(), db, r.URL.Query(), data.ListFilters); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if len(data.ListFilters[3].Options) == 0 {
			data.ListFilters = data.ListFilters[:3]
		}
	} else if adminEditableModel(definition.Model) {
		data.IsSearchableList = true
		data.SearchQuery = r.URL.Query().Get("q")
		if definition.Model == "bookmarkasset" {
			data.UserFilterParam = "status"
			data.UserFilterTitle = "By status"
			data.UserFilter = r.URL.Query().Get("status")
			baseQuery, args = adminAssetListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			data.AllUsersURL = adminListURL(r.URL.Query(), data.UserFilterParam, "")
			statusRows, err := db.QueryContext(r.Context(), `SELECT DISTINCT status FROM bookmarks_bookmarkasset ORDER BY status`)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			for statusRows.Next() {
				var status string
				if err := statusRows.Scan(&status); err != nil {
					statusRows.Close()
					http.Error(w, "Server error", 500)
					return
				}
				data.UserFilters = append(data.UserFilters, adminUserFilter{Username: status, URL: adminListURL(r.URL.Query(), data.UserFilterParam, status), Selected: status == data.UserFilter})
			}
			if err := statusRows.Err(); err != nil {
				statusRows.Close()
				http.Error(w, "Server error", 500)
				return
			}
			statusRows.Close()
		} else if definition.Model == "bookmark" {
			data.IsBookmarkList = true
			baseQuery, args = adminBookmarkListQuery(cfg.DBEngine, data.SearchQuery, r.URL.Query())
			data.ListFilters = adminBookmarkListFilters(r.URL.Query())
			if err := populateBookmarkListFilters(r.Context(), db, r.URL.Query(), data.ListFilters); err != nil {
				http.Error(w, "Server error", 500)
				return
			}
		} else {
			data.UserFilterParam = "owner__username"
			data.UserFilterTitle = "By username"
			if definition.Model == "apitoken" || definition.Model == "feedtoken" {
				data.UserFilterParam = "user__username"
			}
			data.UserFilter = r.URL.Query().Get(data.UserFilterParam)
			switch definition.Model {
			case "toast":
				baseQuery, args = adminToastListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			case "apitoken":
				baseQuery, args = adminAPITokenListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			case "feedtoken":
				baseQuery, args = adminFeedTokenListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			case "tag":
				baseQuery, args = adminTagListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			case "bookmarkbundle":
				baseQuery, args = adminBundleListQuery(cfg.DBEngine, data.SearchQuery, data.UserFilter)
			}
			data.AllUsersURL = adminListURL(r.URL.Query(), data.UserFilterParam, "")
			ownerRows, err := db.QueryContext(r.Context(), `SELECT username FROM auth_user ORDER BY username`)
			if err != nil {
				http.Error(w, "Server error", 500)
				return
			}
			for ownerRows.Next() {
				var name string
				if err := ownerRows.Scan(&name); err != nil {
					ownerRows.Close()
					http.Error(w, "Server error", 500)
					return
				}
				data.UserFilters = append(data.UserFilters, adminUserFilter{Username: name, URL: adminListURL(r.URL.Query(), data.UserFilterParam, name), Selected: name == data.UserFilter})
			}
			if err := ownerRows.Err(); err != nil {
				ownerRows.Close()
				http.Error(w, "Server error", 500)
				return
			}
			ownerRows.Close()
		}
	}
	data.ShowFacets = r.URL.Query().Get("_facets") == "True"
	data.ShowCountsLabel, data.ShowCountsURL = "Show counts", adminListURL(r.URL.Query(), "_facets", "True")
	if data.ShowFacets {
		data.ShowCountsLabel, data.ShowCountsURL = "Hide counts", adminListURL(r.URL.Query(), "_facets", "")
		if err := populateAdminFacetCounts(r.Context(), db, cfg.DBEngine, definition.Model, data.SearchQuery, &data); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	data.HasActiveFilters, data.ClearFiltersURL = adminClearFiltersURL(r.URL.Query(), data)
	var total int64
	if err := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM (`+baseQuery+`) AS records`, args...).Scan(&total); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	pages := max(1, int((total+99)/100))
	page = min(page, pages)
	order := 0
	if requested, err := strconv.Atoi(r.URL.Query().Get("o")); err == nil && requested != 0 && requested >= -len(definition.Columns) && requested <= len(definition.Columns) {
		order = requested
	}
	query := baseQuery
	if order != 0 {
		column := order
		direction := " ASC"
		if column < 0 {
			column = -column
			direction = " DESC"
		}
		query = `SELECT * FROM (` + baseQuery + `) AS ordered_records ORDER BY ` + strconv.Itoa(column+1) + direction + `,1 DESC`
	}
	query += ` LIMIT 100 OFFSET ` + assetMarker(cfg.DBEngine, len(args)+1)
	rows, err := db.QueryContext(r.Context(), query, append(args, (page-1)*100)...)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	defer rows.Close()
	data.Title = adminSelectTitle(data.Language, strings.ToLower(definition.Label))
	data.IsModelList = true
	data.ModelName = definition.Plural
	if definition.App == "auth" && definition.Model == "user" {
		data.Title = adminSelectTitle(data.Language, adminTranslate(data.Language, "user"))
		data.ModelName = adminCapTranslate(data.Language, "users")
	}
	data.AppSlug, data.ModelSlug = definition.App, definition.Model
	data.AppLabel, data.AppPath = "Bookmarks", cfg.URLPrefix()+"admin/"+definition.App+"/"
	if definition.App == "auth" {
		data.AppLabel = adminTranslate(data.Language, "Authentication and Authorization")
	}
	data.PluralLabel = definition.Plural
	if definition.App == "auth" && definition.Model == "user" {
		data.PluralLabel = adminCapTranslate(data.Language, "users")
	}
	if definition.Model == "apitoken" {
		data.PluralLabel = "Api tokens"
	}
	models, err := loadAdminModels(r, db, cfg, user)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	data.ModelColumns = definition.Columns
	for i, label := range definition.Columns {
		if definition.App == "auth" && definition.Model == "user" {
			label = adminCapTranslate(data.Language, strings.ToLower(label))
		}
		index := i + 1
		header := adminListHeader{Label: label, Sortable: !((definition.Model == "bookmark" || definition.Model == "bookmarkasset") && index == 1), URL: adminListURL(r.URL.Query(), "o", strconv.Itoa(index))}
		if order == index || order == -index {
			header.Sorted, header.Ascending = true, order > 0
			header.RemoveURL = adminListURL(r.URL.Query(), "o", "")
			header.ToggleURL = adminListURL(r.URL.Query(), "o", strconv.Itoa(-order))
		} else if order == 0 && ((definition.Model == "tag" && index == 4) || (definition.Model == "bookmark" && index == 5) || (definition.Model == "user" && index == 1) || (definition.Model == "apitoken" && index == 3)) {
			header.Sorted = true
			header.Ascending = definition.Model == "user"
			header.RemoveURL = adminListURL(r.URL.Query(), "o", "")
			header.ToggleURL = header.URL
			if header.Ascending {
				header.ToggleURL = adminListURL(r.URL.Query(), "o", strconv.Itoa(-index))
			}
		}
		data.ModelHeaders = append(data.ModelHeaders, header)
	}
	data.TaskCount = total
	data.Page, data.Pages = page, pages
	if page > 1 {
		data.PreviousPageURL = adminListURL(r.URL.Query(), "p", strconv.Itoa(page-1))
	}
	if page < pages {
		data.NextPageURL = adminListURL(r.URL.Query(), "p", strconv.Itoa(page+1))
	}
	data.CanAdd = permissions.Add
	data.CanDelete = permissions.Delete
	data.IsEditableModel = adminEditableModel(definition.Model)
	data.IsTagList = definition.Model == "tag"
	data.IsUserList = definition.App == "auth" && definition.Model == "user"
	data.IsDefaultActionList = definition.Model == "bookmarkasset" || definition.Model == "bookmarkbundle" || definition.Model == "toast" || definition.Model == "apitoken" || definition.Model == "feedtoken"
	data.AddLabel = "toast"
	if definition.App == "auth" && definition.Model == "user" {
		data.AddLabel = adminTranslate(data.Language, "user")
	} else if definition.Model == "bookmark" {
		data.AddLabel = "bookmark"
	} else if definition.Model == "apitoken" {
		data.AddLabel = "api token"
	} else if definition.Model == "feedtoken" {
		data.AddLabel = "feed token"
	} else if definition.Model == "tag" {
		data.AddLabel = "tag"
	} else if definition.Model == "bookmarkbundle" {
		data.AddLabel = "bookmark bundle"
	} else if definition.Model == "bookmarkasset" {
		data.AddLabel = "bookmark asset"
	}
	data.ModelPath = cfg.URLPrefix() + "admin/" + definition.App + "/" + definition.Model + "/"
	filterSuffix := ""
	if r.URL.RawQuery != "" {
		filterSuffix = "?_changelist_filters=" + url.QueryEscape(r.URL.RawQuery)
	}
	data.AddURL = data.ModelPath + "add/" + filterSuffix
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
		row := adminListRow{ID: adminValueString(values[0], location)}
		if data.IsEditableModel && (permissions.View || permissions.Change) {
			row.Link = data.ModelPath + url.PathEscape(row.ID) + "/change/" + filterSuffix
		}
		for _, value := range values[1:] {
			cell := adminValueStringLocalized(value, location, data.Language)
			if cell == "" {
				cell = "-"
			}
			row.Cells = append(row.Cells, cell)
		}
		if len(row.Cells) > 0 {
			row.ActionLabel = row.Cells[0]
		}
		if definition.Model == "toast" {
			row.ActionLabel = "Toast object (" + row.ID + ")"
		}
		if definition.Model == "bookmark" && len(row.Cells) > 1 {
			urlRunes := []rune(row.Cells[1])
			row.ActionLabel += " (" + string(urlRunes[:min(30, len(urlRunes))]) + "...)"
		} else if definition.Model == "apitoken" && len(row.Cells) > 1 {
			row.ActionLabel += " (" + row.Cells[1] + ")"
		}
		data.ModelRows = append(data.ModelRows, row)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if data.IsTagList || data.IsBookmarkList || data.IsUserList || data.IsDefaultActionList && permissions.Delete {
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
		flashKey := "ld_admin_tag_action"
		if data.IsBookmarkList {
			flashKey = "ld_admin_bookmark_action"
		} else if data.IsUserList {
			flashKey = "ld_admin_user_action"
		} else if data.IsDefaultActionList {
			flashKey = "ld_admin_default_action"
		}
		data.ActionMessage = takeSettingsFlash(w, r, cfg.URLPrefix(), flashKey)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminPageTemplate.Execute(w, data)
	}
}

func adminToastListQuery(engine, search, owner string) (string, []any) {
	return adminFilteredListQuery(engine, `SELECT t.id,t.key,t.message,u.username,CASE WHEN t.acknowledged THEN 'Yes' ELSE 'No' END FROM bookmarks_toast AS t JOIN auth_user AS u ON u.id=t.owner_id`, `t.id DESC`, search, owner, "t.key", "t.message")
}

func adminAPITokenListQuery(engine, search, user string) (string, []any) {
	return adminFilteredListQuery(engine, `SELECT t.id,t.name,u.username,t.created FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id`, `t.created DESC,t.id DESC`, search, user, "t.name", "u.username")
}

func adminFeedTokenListQuery(engine, search, user string) (string, []any) {
	return adminFilteredListQuery(engine, `SELECT t.key AS id,t.key,u.username FROM bookmarks_feedtoken AS t JOIN auth_user AS u ON u.id=t.user_id`, `t.key DESC`, search, user, "t.key")
}

func adminTagListQuery(engine, search, owner string) (string, []any) {
	return adminFilteredListQuery(engine, `SELECT t.id,t.name,(SELECT COUNT(*) FROM bookmarks_bookmark_tags AS bt WHERE bt.tag_id=t.id),u.username,t.date_added FROM bookmarks_tag AS t JOIN auth_user AS u ON u.id=t.owner_id`, `t.date_added DESC,t.id DESC`, search, owner, "t.name", "u.username")
}

func adminBundleListQuery(engine, search, owner string) (string, []any) {
	query := `SELECT t.id,t.name,u.username,t."order",t.search,t.any_tags,t.all_tags,t.excluded_tags,` +
		`CASE t.filter_shared WHEN 'off' THEN 'All' WHEN 'yes' THEN 'Shared' ELSE 'Unshared' END,` +
		`CASE t.filter_unread WHEN 'off' THEN 'All' WHEN 'yes' THEN 'Unread' ELSE 'Read' END,t.date_created ` +
		`FROM bookmarks_bookmarkbundle AS t JOIN auth_user AS u ON u.id=t.owner_id`
	return adminFilteredListQuery(engine, query, `t.id DESC`, search, owner, "t.name", "t.search", "t.any_tags", "t.all_tags", "t.excluded_tags")
}

func adminAssetListQuery(engine, search, status string) (string, []any) {
	base := `SELECT id,COALESCE(NULLIF(display_name,''),'Bookmark Asset #' || id),date_created,status FROM bookmarks_bookmarkasset`
	return adminFilteredListQueryBy(engine, base, `id DESC`, search, status, "status", "display_name", "file")
}

func adminEditableModel(model string) bool {
	return model == "user" || model == "bookmark" || model == "toast" || model == "apitoken" || model == "feedtoken" || model == "tag" || model == "bookmarkbundle" || model == "bookmarkasset"
}

func adminUserListQuery(engine, search string, params url.Values) (string, []any) {
	query := `SELECT DISTINCT u.id,u.username,u.email,u.first_name,u.last_name,u.is_staff FROM auth_user AS u LEFT JOIN auth_user_groups AS ug ON ug.user_id=u.id LEFT JOIN auth_group AS g ON g.id=ug.group_id`
	var conditions []string
	var args []any
	bind := func(value any) string {
		args = append(args, value)
		return assetMarker(engine, len(args))
	}
	for _, field := range []struct{ Param, Column string }{{"is_staff__exact", "u.is_staff"}, {"is_superuser__exact", "u.is_superuser"}, {"is_active__exact", "u.is_active"}} {
		if value := params.Get(field.Param); value == "0" || value == "1" {
			conditions = append(conditions, field.Column+" = "+bind(value == "1"))
		}
	}
	if groupID := params.Get("groups__id__exact"); groupID != "" {
		if _, err := strconv.ParseInt(groupID, 10, 64); err == nil {
			conditions = append(conditions, "g.id = "+bind(groupID))
		}
	}
	for _, term := range splitAdminSearch(search) {
		var alternatives []string
		for _, field := range []string{"u.username", "u.first_name", "u.last_name", "u.email"} {
			if engine == "postgres" {
				escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term)
				alternatives = append(alternatives, field+" ILIKE "+bind("%"+escaped+"%")+` ESCAPE '\'`)
			} else {
				alternatives = append(alternatives, "ld_ci_contains("+field+", "+bind(term)+") = 1")
			}
		}
		conditions = append(conditions, "("+strings.Join(alternatives, " OR ")+")")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	return query + " ORDER BY u.username", args
}

func adminUserListFilters(params url.Values) []adminFilterGroup {
	groups := []adminFilterGroup{
		{Title: "By staff status", Param: "is_staff__exact", Value: params.Get("is_staff__exact")},
		{Title: "By superuser status", Param: "is_superuser__exact", Value: params.Get("is_superuser__exact")},
		{Title: "By active", Param: "is_active__exact", Value: params.Get("is_active__exact")},
		{Title: "By groups", Param: "groups__id__exact", Value: params.Get("groups__id__exact")},
	}
	for i := range groups {
		groups[i].AllURL = adminListURL(params, groups[i].Param, "")
	}
	for i := 0; i < 3; i++ {
		for _, option := range []adminUserFilter{{Username: "Yes", URL: "1"}, {Username: "No", URL: "0"}} {
			groups[i].Options = append(groups[i].Options, adminUserFilter{Username: option.Username, URL: adminListURL(params, groups[i].Param, option.URL), Selected: groups[i].Value == option.URL})
		}
	}
	return groups
}

func populateAdminUserListFilters(ctx context.Context, db *sql.DB, params url.Values, groups []adminFilterGroup) error {
	rows, err := db.QueryContext(ctx, `SELECT id,name FROM auth_group ORDER BY name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		groups[3].Options = append(groups[3].Options, adminUserFilter{Username: name, URL: adminListURL(params, groups[3].Param, id), Selected: id == groups[3].Value})
	}
	return rows.Err()
}

func adminBookmarkListQuery(engine, search string, params url.Values) (string, []any) {
	query := `SELECT DISTINCT b.id,COALESCE(NULLIF(b.title,''),b.url),b.url,b.is_archived,u.username,b.date_added FROM bookmarks_bookmark AS b JOIN auth_user AS u ON u.id=b.owner_id LEFT JOIN bookmarks_bookmark_tags AS bt ON bt.bookmark_id=b.id LEFT JOIN bookmarks_tag AS t ON t.id=bt.tag_id`
	var conditions []string
	var args []any
	bind := func(value any) string {
		args = append(args, value)
		return assetMarker(engine, len(args))
	}
	if owner := params.Get("owner__username"); owner != "" {
		conditions = append(conditions, "u.username = "+bind(owner))
	}
	for _, field := range []struct{ Param, Column string }{{"is_archived__exact", "is_archived"}, {"unread__exact", "unread"}} {
		if value := params.Get(field.Param); value == "0" || value == "1" {
			conditions = append(conditions, "b."+field.Column+" = "+bind(value == "1"))
		}
	}
	if tagID := params.Get("tags__id__exact"); tagID != "" {
		if _, err := strconv.ParseInt(tagID, 10, 64); err == nil {
			conditions = append(conditions, "t.id = "+bind(tagID))
		}
	} else if params.Get("tags__isnull") == "True" {
		conditions = append(conditions, "t.id IS NULL")
	}
	for _, term := range splitAdminSearch(search) {
		var alternatives []string
		for _, field := range []string{"b.title", "b.description", "b.website_title", "b.website_description", "b.url", "t.name"} {
			if engine == "postgres" {
				escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term)
				alternatives = append(alternatives, field+" ILIKE "+bind("%"+escaped+"%")+` ESCAPE '\'`)
			} else {
				alternatives = append(alternatives, "ld_ci_contains("+field+", "+bind(term)+") = 1")
			}
		}
		conditions = append(conditions, "("+strings.Join(alternatives, " OR ")+")")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	return query + " ORDER BY b.date_added DESC", args
}

func adminBookmarkListFilters(params url.Values) []adminFilterGroup {
	groups := []adminFilterGroup{
		{Title: "By username", Param: "owner__username", Value: params.Get("owner__username")},
		{Title: "By is archived", Param: "is_archived__exact", Value: params.Get("is_archived__exact")},
		{Title: "By unread", Param: "unread__exact", Value: params.Get("unread__exact")},
		{Title: "By tags", Param: "tags__id__exact", Value: params.Get("tags__id__exact"), ExtraParam: "tags__isnull", ExtraValue: params.Get("tags__isnull"), IsNull: params.Get("tags__isnull") == "True"},
	}
	// Owner and tag options are populated by the list handler from their tables.
	for i := range groups {
		groups[i].AllURL = adminListURL(params, groups[i].Param, "")
	}
	groups[3].AllURL = adminBookmarkTagListURL(params, "", "")
	for i := 1; i <= 2; i++ {
		for _, option := range []adminUserFilter{{Username: "Yes", URL: "1"}, {Username: "No", URL: "0"}} {
			groups[i].Options = append(groups[i].Options, adminUserFilter{Username: option.Username, URL: adminListURL(params, groups[i].Param, option.URL), Selected: groups[i].Value == option.URL})
		}
	}
	return groups
}

func populateBookmarkListFilters(ctx context.Context, db *sql.DB, params url.Values, groups []adminFilterGroup) error {
	ownerRows, err := db.QueryContext(ctx, `SELECT username FROM auth_user ORDER BY username`)
	if err != nil {
		return err
	}
	defer ownerRows.Close()
	for ownerRows.Next() {
		var username string
		if err := ownerRows.Scan(&username); err != nil {
			return err
		}
		groups[0].Options = append(groups[0].Options, adminUserFilter{Username: username, URL: adminListURL(params, groups[0].Param, username), Selected: username == groups[0].Value})
	}
	if err := ownerRows.Err(); err != nil {
		return err
	}
	tagRows, err := db.QueryContext(ctx, `SELECT id,name FROM bookmarks_tag ORDER BY date_added DESC,id DESC`)
	if err != nil {
		return err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var id, name string
		if err := tagRows.Scan(&id, &name); err != nil {
			return err
		}
		url := adminBookmarkTagListURL(params, id, "")
		groups[3].Options = append(groups[3].Options, adminUserFilter{Username: name, URL: url, Selected: id == groups[3].Value})
	}
	if err := tagRows.Err(); err != nil {
		return err
	}
	untaggedURL := adminBookmarkTagListURL(params, "", "True")
	groups[3].Options = append(groups[3].Options, adminUserFilter{Username: "-", URL: untaggedURL, Selected: params.Get("tags__isnull") == "True"})
	return nil
}

func adminFilteredListQuery(engine, query, order, search, user string, searchFields ...string) (string, []any) {
	return adminFilteredListQueryBy(engine, query, order, search, user, "u.username", searchFields...)
}

func adminFilteredListQueryBy(engine, query, order, search, filterValue, filterField string, searchFields ...string) (string, []any) {
	var conditions []string
	var args []any
	bind := func(value any) string {
		args = append(args, value)
		return assetMarker(engine, len(args))
	}
	if filterValue != "" {
		conditions = append(conditions, filterField+" = "+bind(filterValue))
	}
	for _, term := range splitAdminSearch(search) {
		var fieldConditions []string
		if engine == "postgres" {
			escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(term)
			for _, field := range searchFields {
				fieldConditions = append(fieldConditions, field+" ILIKE "+bind("%"+escaped+"%")+` ESCAPE '\'`)
			}
		} else {
			for _, field := range searchFields {
				fieldConditions = append(fieldConditions, "ld_ci_contains("+field+", "+bind(term)+") = 1")
			}
		}
		conditions = append(conditions, "("+strings.Join(fieldConditions, " OR ")+")")
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	return query + " ORDER BY " + order, args
}

func splitAdminSearch(search string) []string {
	terms := adminSearchSplit.FindAllString(search, -1)
	for i, term := range terms {
		if len(term) > 1 && (term[0] == '\'' || term[0] == '"') && term[len(term)-1] == term[0] {
			quote := string(term[0])
			term = strings.ReplaceAll(term[1:len(term)-1], "\\"+quote, quote)
			terms[i] = strings.ReplaceAll(term, `\\`, `\`)
		}
	}
	return terms
}

func adminBookmarkTagListURL(current url.Values, tagID, isNull string) string {
	params := url.Values{}
	for name, values := range current {
		if name != "p" && name != "tags__id__exact" && name != "tags__isnull" {
			params[name] = append([]string(nil), values...)
		}
	}
	if tagID != "" {
		params.Set("tags__id__exact", tagID)
	}
	if isNull != "" {
		params.Set("tags__isnull", isNull)
	}
	if encoded := params.Encode(); encoded != "" {
		return "?" + encoded
	}
	return "?"
}

func adminListURL(current url.Values, key, value string) string {
	params := url.Values{}
	for name, values := range current {
		if name == "p" {
			continue
		}
		params[name] = append([]string(nil), values...)
	}
	if value == "" {
		params.Del(key)
	} else {
		params.Set(key, value)
	}
	if encoded := params.Encode(); encoded != "" {
		return "?" + encoded
	}
	return "?"
}

func adminValueString(value any, location *time.Location) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(typed)
	case time.Time:
		value := typed.In(location)
		months := [...]string{"Jan.", "Feb.", "March", "April", "May", "June", "July", "Aug.", "Sept.", "Oct.", "Nov.", "Dec."}
		date := fmt.Sprintf("%s %d, %d, ", months[value.Month()-1], value.Day(), value.Year())
		if value.Hour() == 0 && value.Minute() == 0 {
			return date + "midnight"
		}
		if value.Hour() == 12 && value.Minute() == 0 {
			return date + "noon"
		}
		period := "a.m."
		if value.Hour() >= 12 {
			period = "p.m."
		}
		return date + fmt.Sprintf("%d:%02d %s", (value.Hour()+11)%12+1, value.Minute(), period)
	case bool:
		if typed {
			return "Yes"
		}
		return "No"
	default:
		return fmt.Sprint(value)
	}
}

func adminValueStringLocalized(value any, location *time.Location, language string) string {
	if date, ok := value.(time.Time); ok && language == "ru" {
		date = date.In(location)
		return fmt.Sprintf("%d %s %d г. %d:%02d", date.Day(), russianBookmarkMonths[date.Month()-1], date.Year(), date.Hour(), date.Minute())
	}
	return adminValueString(value, location)
}
