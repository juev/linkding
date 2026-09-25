package httpserver

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	{App: "auth", Model: "user", Label: "User", Plural: "Users", Query: `SELECT id,username,email,is_staff,is_active FROM auth_user ORDER BY id DESC`, Columns: []string{"Username", "Email address", "Staff status", "Active"}},
	{App: "bookmarks", Model: "bookmark", Label: "Bookmark", Plural: "Bookmarks", Query: `SELECT b.id,COALESCE(NULLIF(b.title,''),b.url),b.url,b.is_archived,u.username,b.date_added FROM bookmarks_bookmark AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.date_added DESC,b.id DESC`, Columns: []string{"Title", "URL", "Is archived", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkasset", Label: "Bookmark asset", Plural: "Bookmark assets", Query: `SELECT id,display_name,date_created,status FROM bookmarks_bookmarkasset ORDER BY id DESC`, Columns: []string{"Display name", "Date created", "Status"}},
	{App: "bookmarks", Model: "tag", Label: "Tag", Plural: "Tags", Query: `SELECT t.id,t.name,(SELECT COUNT(*) FROM bookmarks_bookmark_tags AS bt WHERE bt.tag_id=t.id),u.username,t.date_added FROM bookmarks_tag AS t JOIN auth_user AS u ON u.id=t.owner_id ORDER BY t.date_added DESC,t.id DESC`, Columns: []string{"Name", "Bookmarks count", "Owner", "Date added"}},
	{App: "bookmarks", Model: "bookmarkbundle", Label: "Bookmark bundle", Plural: "Bookmark bundles", Query: `SELECT b.id,b.name,u.username,b."order",b.search,b.any_tags,b.all_tags,b.excluded_tags,b.filter_shared,b.filter_unread,b.date_created FROM bookmarks_bookmarkbundle AS b JOIN auth_user AS u ON u.id=b.owner_id ORDER BY b.id DESC`, Columns: []string{"Name", "Owner", "Order", "Search", "Any tags", "All tags", "Excluded tags", "Filter shared", "Filter unread", "Date created"}},
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

type adminUserFilter struct {
	Username, URL string
	Selected      bool
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
			appLabel = "Authentication and Authorization"
		}
		link := adminModelLink{Label: definition.Plural, AppLabel: appLabel}
		base := cfg.URLPrefix() + "admin/" + definition.App + "/" + definition.Model + "/"
		if permissions.canList() {
			link.Path = base
		}
		if canAdd {
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
	if adminEditableModel(definition.Model) {
		data.IsSearchableList = true
		data.SearchQuery = r.URL.Query().Get("q")
		data.UserFilterParam = "owner__username"
		data.UserFilterTitle = "By owner username"
		if definition.Model == "apitoken" || definition.Model == "feedtoken" {
			data.UserFilterParam = "user__username"
			data.UserFilterTitle = "By user username"
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
	var total int64
	if err := db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM (`+baseQuery+`) AS records`, args...).Scan(&total); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	pages := max(1, int((total+99)/100))
	page = min(page, pages)
	query := baseQuery + ` LIMIT 100 OFFSET ` + assetMarker(cfg.DBEngine, len(args)+1)
	rows, err := db.QueryContext(r.Context(), query, append(args, (page-1)*100)...)
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
	data.AddLabel = "toast"
	if definition.Model == "apitoken" {
		data.AddLabel = "API token"
	} else if definition.Model == "feedtoken" {
		data.AddLabel = "feed token"
	} else if definition.Model == "tag" {
		data.AddLabel = "tag"
	} else if definition.Model == "bookmarkbundle" {
		data.AddLabel = "bookmark bundle"
	}
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
		row := adminListRow{ID: adminValueString(values[0], location)}
		if data.IsEditableModel && (permissions.View || permissions.Change) {
			row.Link = data.ModelPath + url.PathEscape(row.ID) + "/change/"
		}
		for _, value := range values[1:] {
			row.Cells = append(row.Cells, adminValueString(value, location))
		}
		data.ModelRows = append(data.ModelRows, row)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if data.IsTagList {
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
		data.ActionMessage = takeSettingsFlash(w, r, cfg.URLPrefix(), "ld_admin_tag_action")
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
	return adminFilteredListQuery(engine, `SELECT t.key,t.key,u.username FROM bookmarks_feedtoken AS t JOIN auth_user AS u ON u.id=t.user_id`, `t.created DESC`, search, user, "t.key")
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

func adminEditableModel(model string) bool {
	return model == "toast" || model == "apitoken" || model == "feedtoken" || model == "tag" || model == "bookmarkbundle"
}

func adminFilteredListQuery(engine, query, order, search, user string, searchFields ...string) (string, []any) {
	var conditions []string
	var args []any
	bind := func(value any) string {
		args = append(args, value)
		return assetMarker(engine, len(args))
	}
	if user != "" {
		conditions = append(conditions, "u.username = "+bind(user))
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
