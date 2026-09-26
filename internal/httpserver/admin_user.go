package httpserver

import (
	"database/sql"
	"embed"
	"errors"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/settings"
)

//go:embed admin_user.html admin_sidebar.html
var adminUserFile embed.FS
var adminUserTemplate = adminSidebarTemplate(adminUserFile, "admin_user.html")

type adminUserOption struct {
	ID       int64
	Label    string
	Selected bool
}

type adminUserData struct {
	Language                                                                          string
	Prefix, Title, Username, CSRFToken, Action, ListURL                               string
	UserName, FirstName, LastName, Email, PasswordHash                                string
	PasswordAlgorithm, PasswordIterations, PasswordSaltSummary, PasswordDigestSummary string
	Password1, Password2, Error                                                       string
	LastLoginDate, LastLoginTime                                                      string
	DateJoinedDate, DateJoinedTime                                                    string
	ID                                                                                int64
	IsActive, IsStaff, IsSuperuser                                                    bool
	UsablePassword, IsAdd, CanChange, CanDelete                                       bool
	Groups, Permissions                                                               []adminUserOption
	GroupIDs, PermissionIDs                                                           map[int64]bool
	Profile                                                                           adminUserProfileData
	DashboardApps                                                                     []adminDashboardApp
}

func serveAdminUser(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository, user auth.User, permissions adminPermissions) {
	base := cfg.URLPrefix() + "admin/auth/user/"
	part := strings.TrimPrefix(r.URL.Path, base)
	var action string
	var id int64
	if part == "add/" {
		action = "add"
	} else {
		pieces := strings.Split(part, "/")
		if len(pieces) != 3 || pieces[2] != "" || (pieces[1] != "change" && pieces[1] != "password" && pieces[1] != "delete") {
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
	if action == "password" {
		serveAdminUserPassword(w, r, cfg, db, users, user, permissions, id)
		return
	}
	if action == "delete" {
		serveAdminUserDelete(w, r, cfg, db, user, permissions, id)
		return
	}
	if (action == "add" && !permissions.Add) || (action == "change" && !permissions.Change && !permissions.View) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	location, err := adminTagLocation(cfg.TimeZone)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data := adminUserData{Prefix: cfg.URLPrefix(), Title: "Add user", Username: user.Username, Action: r.URL.Path, ListURL: base, ID: id, IsAdd: action == "add", UsablePassword: true, CanChange: action == "add" || permissions.Change, CanDelete: permissions.Delete, GroupIDs: map[int64]bool{}, PermissionIDs: map[int64]bool{}}
	if action == "change" {
		data.Title = "Change user"
		if !permissions.Change {
			data.Title = "View user"
		}
		var lastLogin sql.NullTime
		var joined time.Time
		err := db.QueryRowContext(r.Context(), `SELECT username,password,first_name,last_name,email,is_active,is_staff,is_superuser,last_login,date_joined FROM auth_user WHERE id = `+assetMarker(cfg.DBEngine, 1), id).Scan(&data.UserName, &data.PasswordHash, &data.FirstName, &data.LastName, &data.Email, &data.IsActive, &data.IsStaff, &data.IsSuperuser, &lastLogin, &joined)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.PasswordAlgorithm, data.PasswordIterations, data.PasswordSaltSummary, data.PasswordDigestSummary = adminUserPasswordSummary(data.PasswordHash)
		data.DateJoinedDate, data.DateJoinedTime = adminBookmarkDateParts(joined, location)
		if lastLogin.Valid {
			data.LastLoginDate, data.LastLoginTime = adminBookmarkDateParts(lastLogin.Time, location)
		}
		if err := data.loadRelations(r, cfg, db); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		data.Profile, err = loadAdminUserProfile(r.Context(), db, cfg.DBEngine, id)
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		if !verifyAPICSRF(r, cfg) {
			writeCSRFFailure(w, r)
			return
		}
		if action == "change" && !permissions.Change {
			http.Error(w, "Forbidden", 403)
			return
		}
		previous := data
		data.readPost(r, action)
		if err := data.validate(r, cfg, db, location, action); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		if data.Error == "" {
			savedID := id
			if action == "add" {
				input := auth.NewUser{Username: data.UserName, Password: data.Password1}
				tx, txErr := db.BeginTx(r.Context(), nil)
				if txErr != nil {
					http.Error(w, "Server error", 500)
					return
				}
				defer tx.Rollback()
				var created auth.User
				created, err = users.CreateUserTx(r.Context(), tx, input, data.UsablePassword)
				if err == nil {
					savedID = created.ID
					err = writeAdminLog(r.Context(), tx, cfg.DBEngine, user.ID, "auth", "user", strconv.FormatInt(created.ID, 10), created.Username, 1, adminAdditionMessage)
				}
				if err == nil {
					err = tx.Commit()
				}
			} else {
				err = data.saveChange(r, cfg, db, location, user.ID, &previous)
			}
			if err != nil {
				var profileError settings.ValidationError
				if errors.As(err, &profileError) {
					data.Error = "Invalid profile field: " + profileError.Field + "."
				} else {
					http.Error(w, "Server error", 500)
					return
				}
			} else {
				redirect := base
				if _, ok := r.PostForm["_addanother"]; ok {
					redirect = base + "add/"
				} else if _, ok := r.PostForm["_continue"]; ok {
					redirect = base + strconv.FormatInt(savedID, 10) + "/change/"
				}
				writeRedirect(w, r, redirect)
				return
			}
		}
	}
	secret := ""
	if cookie, err := r.Cookie(auth.CSRFCookieName); err == nil && auth.VerifyCSRF(cookie.Value, cookie.Value) {
		secret = cookie.Value
	}
	if secret == "" {
		secret, err = auth.NewCSRFSecret()
		if err != nil {
			http.Error(w, "Server error", 500)
			return
		}
		setCSRFCookie(w, cfg.URLPrefix(), secret)
	}
	data.CSRFToken, err = auth.MaskCSRF(secret)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	if action == "change" {
		if err := data.loadOptions(r, db); err != nil {
			http.Error(w, "Server error", 500)
			return
		}
	}
	models, err := loadAdminModels(r, db, cfg, user)
	if err != nil {
		http.Error(w, "Server error", 500)
		return
	}
	data.Language = selectedAdminLanguage(r).Code
	data.DashboardApps = groupAdminDashboardApps(cfg, models)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	if r.Method != http.MethodHead {
		_ = adminUserTemplate.Execute(w, data)
	}
}

func adminUserPasswordSummary(hash string) (algorithm, iterations, salt, digest string) {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 {
		return "", "", "", ""
	}
	mask := func(value string) string {
		visible := min(6, len(value))
		return value[:visible] + strings.Repeat("*", len(value)-visible)
	}
	return parts[0], parts[1], mask(parts[2]), mask(parts[3])
}

func (data *adminUserData) loadRelations(r *http.Request, cfg config.Config, db *sql.DB) error {
	for _, relation := range []struct {
		Table, Field string
		Selected     map[int64]bool
	}{{"auth_user_groups", "group_id", data.GroupIDs}, {"auth_user_user_permissions", "permission_id", data.PermissionIDs}} {
		rows, err := db.QueryContext(r.Context(), `SELECT `+relation.Field+` FROM `+relation.Table+` WHERE user_id = `+assetMarker(cfg.DBEngine, 1), data.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			relation.Selected[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (data *adminUserData) readPost(r *http.Request, action string) {
	data.UserName = strings.TrimSpace(r.PostForm.Get("username"))
	if action == "add" {
		data.UsablePassword = r.PostForm.Get("usable_password") != "false"
		data.Password1, data.Password2 = r.PostForm.Get("password1"), r.PostForm.Get("password2")
		return
	}
	data.FirstName = strings.TrimSpace(r.PostForm.Get("first_name"))
	data.LastName = strings.TrimSpace(r.PostForm.Get("last_name"))
	data.Email = strings.TrimSpace(r.PostForm.Get("email"))
	data.IsActive = r.PostForm.Has("is_active")
	data.IsStaff = r.PostForm.Has("is_staff")
	data.IsSuperuser = r.PostForm.Has("is_superuser")
	data.LastLoginDate, data.LastLoginTime = r.PostForm.Get("last_login_0"), r.PostForm.Get("last_login_1")
	data.DateJoinedDate, data.DateJoinedTime = r.PostForm.Get("date_joined_0"), r.PostForm.Get("date_joined_1")
	data.GroupIDs = parseAdminUserIDs(r.PostForm["groups"], &data.Error)
	data.PermissionIDs = parseAdminUserIDs(r.PostForm["user_permissions"], &data.Error)
	data.Profile = adminUserProfileFromPost(r.PostForm, data.Profile)
}

func parseAdminUserIDs(raw []string, errorText *string) map[int64]bool {
	result := make(map[int64]bool)
	for _, value := range raw {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			*errorText = "Select valid groups and permissions."
			continue
		}
		result[id] = true
	}
	return result
}

func validAdminUsername(value string) bool {
	if value == "" || len([]rune(value)) > 150 {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@.+-", r) {
			return false
		}
	}
	return true
}

func (data *adminUserData) validate(r *http.Request, cfg config.Config, db *sql.DB, location *time.Location, action string) error {
	if data.Error != "" {
		return nil
	}
	if !validAdminUsername(data.UserName) {
		data.Error = "Enter a valid username of at most 150 characters."
		return nil
	}
	var existing int64
	query := `SELECT id FROM auth_user WHERE username = ` + assetMarker(cfg.DBEngine, 1)
	err := db.QueryRowContext(r.Context(), query, data.UserName).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing != data.ID {
		data.Error = "A user with that username already exists."
		return nil
	}
	if action == "add" {
		if data.UsablePassword {
			if data.Password1 == "" || data.Password2 == "" || data.Password1 != data.Password2 {
				data.Error = "The two password fields didn’t match."
				return nil
			}
			if err := auth.ValidateNewPassword(data.Password2, data.UserName); err != nil {
				data.Error = err.Error()
			}
		}
		return nil
	}
	if len([]rune(data.FirstName)) > 150 || len([]rune(data.LastName)) > 150 || len([]rune(data.Email)) > 254 {
		data.Error = "Personal information exceeds its maximum length."
		return nil
	}
	if data.Email != "" {
		address, err := mail.ParseAddress(data.Email)
		if err != nil || address.Address != data.Email {
			data.Error = "Enter a valid email address."
			return nil
		}
	}
	if _, err := parseAdminTagDateTime(data.DateJoinedDate, data.DateJoinedTime, location); err != nil {
		data.Error = "Enter a valid date joined."
		return nil
	}
	if data.LastLoginDate != "" || data.LastLoginTime != "" {
		if _, err := parseAdminTagDateTime(data.LastLoginDate, data.LastLoginTime, location); err != nil {
			data.Error = "Enter a valid last login."
			return nil
		}
	}
	if r.PostForm.Get("profile-TOTAL_FORMS") != "1" || r.PostForm.Get("profile-INITIAL_FORMS") != "1" {
		data.Error = "Profile form data is missing."
		return nil
	}
	if len([]rune(data.Profile.CustomCSSHash)) > 32 {
		data.Error = "Profile CSS hash is too long."
		return nil
	}
	for _, relation := range []struct {
		Table string
		IDs   map[int64]bool
	}{{"auth_group", data.GroupIDs}, {"auth_permission", data.PermissionIDs}} {
		for id := range relation.IDs {
			var exists bool
			query := `SELECT EXISTS(SELECT 1 FROM ` + relation.Table + ` WHERE id = ` + assetMarker(cfg.DBEngine, 1) + `)`
			if err := db.QueryRowContext(r.Context(), query, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				data.Error = "Select valid groups and permissions."
				return nil
			}
		}
	}
	return nil
}

func (data *adminUserData) saveChange(r *http.Request, cfg config.Config, db *sql.DB, location *time.Location, actorID int64, previous *adminUserData) error {
	joined, _ := parseAdminTagDateTime(data.DateJoinedDate, data.DateJoinedTime, location)
	var lastLogin any
	if data.LastLoginDate != "" {
		value, _ := parseAdminTagDateTime(data.LastLoginDate, data.LastLoginTime, location)
		lastLogin = value.UTC()
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := []any{data.UserName, data.FirstName, data.LastName, data.Email, data.IsActive, data.IsStaff, data.IsSuperuser, lastLogin, joined.UTC(), data.ID}
	query := `UPDATE auth_user SET username=` + assetMarker(cfg.DBEngine, 1) + `,first_name=` + assetMarker(cfg.DBEngine, 2) + `,last_name=` + assetMarker(cfg.DBEngine, 3) + `,email=` + assetMarker(cfg.DBEngine, 4) + `,is_active=` + assetMarker(cfg.DBEngine, 5) + `,is_staff=` + assetMarker(cfg.DBEngine, 6) + `,is_superuser=` + assetMarker(cfg.DBEngine, 7) + `,last_login=` + assetMarker(cfg.DBEngine, 8) + `,date_joined=` + assetMarker(cfg.DBEngine, 9) + ` WHERE id=` + assetMarker(cfg.DBEngine, 10)
	if _, err := tx.ExecContext(r.Context(), query, values...); err != nil {
		return err
	}
	for _, relation := range []struct {
		Table, Field string
		IDs          map[int64]bool
	}{{"auth_user_groups", "group_id", data.GroupIDs}, {"auth_user_user_permissions", "permission_id", data.PermissionIDs}} {
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM `+relation.Table+` WHERE user_id=`+assetMarker(cfg.DBEngine, 1), data.ID); err != nil {
			return err
		}
		ids := make([]int64, 0, len(relation.IDs))
		for id := range relation.IDs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			query := `INSERT INTO ` + relation.Table + `(user_id,` + relation.Field + `) VALUES (` + assetMarker(cfg.DBEngine, 1) + `,` + assetMarker(cfg.DBEngine, 2) + `)`
			if _, err := tx.ExecContext(r.Context(), query, data.ID, id); err != nil {
				return err
			}
		}
	}
	if err := settings.UpdateProfileTx(r.Context(), tx, cfg.DBEngine, data.ID, data.Profile.Form); err != nil {
		return err
	}
	if err := writeAdminLog(r.Context(), tx, cfg.DBEngine, actorID, "auth", "user", strconv.FormatInt(data.ID, 10), data.UserName, 2, adminUserChangeMessage(*previous, *data)); err != nil {
		return err
	}
	return tx.Commit()
}

func (data *adminUserData) loadOptions(r *http.Request, db *sql.DB) error {
	groups, err := db.QueryContext(r.Context(), `SELECT id,name FROM auth_group ORDER BY name`)
	if err != nil {
		return err
	}
	for groups.Next() {
		var option adminUserOption
		if err := groups.Scan(&option.ID, &option.Label); err != nil {
			groups.Close()
			return err
		}
		option.Selected = data.GroupIDs[option.ID]
		data.Groups = append(data.Groups, option)
	}
	err = groups.Err()
	groups.Close()
	if err != nil {
		return err
	}
	permissions, err := db.QueryContext(r.Context(), `SELECT p.id,c.app_label,c.model,p.name FROM auth_permission AS p JOIN django_content_type AS c ON c.id=p.content_type_id ORDER BY c.app_label,c.model,p.name`)
	if err != nil {
		return err
	}
	for permissions.Next() {
		var option adminUserOption
		var app, model, name string
		if err := permissions.Scan(&option.ID, &app, &model, &name); err != nil {
			permissions.Close()
			return err
		}
		option.Label = adminUserPermissionLabel(app, model, name)
		option.Selected = data.PermissionIDs[option.ID]
		data.Permissions = append(data.Permissions, option)
	}
	err = permissions.Err()
	permissions.Close()
	return err
}

func adminUserPermissionLabel(app, model, name string) string {
	appLabels := map[string]string{
		"admin": "Administration", "auth": "Authentication and Authorization", "authtoken": "Auth Token",
		"bookmarks": "Bookmarks", "contenttypes": "Content Types", "sessions": "Sessions",
	}
	modelLabels := map[string]string{
		"logentry": "log entry", "bookmarkasset": "bookmark asset", "bookmarkbundle": "bookmark bundle",
		"feedtoken": "feed token", "apitoken": "api token", "globalsettings": "global settings",
		"userprofile": "user profile", "contenttype": "content type", "tokenproxy": "Token", "token": "Token",
	}
	appLabel := appLabels[app]
	if appLabel == "" {
		appLabel = app
	}
	modelLabel := modelLabels[model]
	if modelLabel == "" {
		modelLabel = model
	}
	return appLabel + " | " + modelLabel + " | " + name
}
