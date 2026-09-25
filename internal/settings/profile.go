package settings

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/jobs"
)

type ProfileField struct {
	Name    string
	Kind    string
	Choices []string
}

var ProfileFields = []ProfileField{
	{"theme", "choice", []string{"auto", "light", "dark"}},
	{"bookmark_date_display", "choice", []string{"relative", "absolute", "hidden"}},
	{"bookmark_description_display", "choice", []string{"inline", "separate"}},
	{"bookmark_description_max_lines", "number", nil},
	{"bookmark_link_target", "choice", []string{"_blank", "_self"}},
	{"web_archive_integration", "choice", []string{"disabled", "enabled"}},
	{"tag_search", "choice", []string{"strict", "lax"}},
	{"tag_grouping", "choice", []string{"alphabetical", "disabled"}},
	{"enable_sharing", "checkbox", nil},
	{"enable_public_sharing", "checkbox", nil},
	{"enable_favicons", "checkbox", nil},
	{"enable_preview_images", "checkbox", nil},
	{"enable_automatic_html_snapshots", "checkbox", nil},
	{"display_url", "checkbox", nil},
	{"display_view_bookmark_action", "checkbox", nil},
	{"display_edit_bookmark_action", "checkbox", nil},
	{"display_archive_bookmark_action", "checkbox", nil},
	{"display_remove_bookmark_action", "checkbox", nil},
	{"permanent_notes", "checkbox", nil},
	{"default_mark_unread", "checkbox", nil},
	{"default_mark_shared", "checkbox", nil},
	{"sticky_pagination", "checkbox", nil},
	{"collapse_side_panel", "checkbox", nil},
	{"hide_bundles", "checkbox", nil},
	{"legacy_search", "checkbox", nil},
	{"custom_css", "text", nil},
	{"auto_tagging_rules", "text", nil},
	{"items_per_page", "number", nil},
}

type ValidationError struct{ Field string }

func (e ValidationError) Error() string { return "invalid profile field: " + e.Field }

func marker(engine string, index int) string {
	if engine == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

// LoadProfileForm returns the full upstream settings form state, including
// fields outside the smaller REST profile serializer.
func LoadProfileForm(ctx context.Context, db *sql.DB, engine string, userID int64) (url.Values, error) {
	columns := make([]string, len(ProfileFields))
	scans := make([]any, len(ProfileFields))
	values := make([]any, len(ProfileFields))
	for index, field := range ProfileFields {
		columns[index] = field.Name
		scans[index] = &values[index]
	}
	query := "SELECT " + strings.Join(columns, ",") + " FROM bookmarks_userprofile WHERE user_id = " + marker(engine, 1)
	if err := db.QueryRowContext(ctx, query, userID).Scan(scans...); err != nil {
		return nil, err
	}
	form := url.Values{}
	for index, field := range ProfileFields {
		value := values[index]
		if field.Kind == "checkbox" {
			if value == true || value == int64(1) {
				form.Set(field.Name, "on")
			}
			continue
		}
		if bytes, ok := value.([]byte); ok {
			form.Set(field.Name, string(bytes))
		} else {
			form.Set(field.Name, fmt.Sprint(value))
		}
	}
	return form, nil
}

// UpdateProfile applies the same fields as UserProfileForm, preserving fields
// that are not in the form. Checkbox omissions mean false, as in Django forms.
func UpdateProfile(ctx context.Context, db *sql.DB, engine string, userID int64, form url.Values) error {
	return updateProfile(ctx, db, engine, userID, form)
}

// UpdateProfileTx saves an admin inline together with its parent user and M2M rows.
func UpdateProfileTx(ctx context.Context, tx *sql.Tx, engine string, userID int64, form url.Values) error {
	return updateProfile(ctx, tx, engine, userID, form)
}

type profileExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func updateProfile(ctx context.Context, executor profileExecutor, engine string, userID int64, form url.Values) error {
	columns := make([]string, 0, len(ProfileFields)+1)
	values := make([]any, 0, len(ProfileFields)+2)
	for _, field := range ProfileFields {
		value := form.Get(field.Name)
		switch field.Kind {
		case "choice":
			valid := false
			for _, option := range field.Choices {
				if value == option {
					valid = true
					break
				}
			}
			if !valid {
				return ValidationError{Field: field.Name}
			}
			values = append(values, value)
		case "checkbox":
			values = append(values, value != "" && value != "0" && !strings.EqualFold(value, "false"))
		case "number":
			parsed, err := strconv.Atoi(value)
			if err != nil || field.Name == "items_per_page" && parsed < 10 {
				return ValidationError{Field: field.Name}
			}
			values = append(values, parsed)
		case "text":
			values = append(values, value)
		}
		columns = append(columns, field.Name+" = "+marker(engine, len(values)))
	}
	css := form.Get("custom_css")
	cssHash := ""
	if css != "" {
		sum := md5.Sum([]byte(css))
		cssHash = hex.EncodeToString(sum[:])
	}
	values = append(values, cssHash)
	columns = append(columns, "custom_css_hash = "+marker(engine, len(values)))
	values = append(values, userID)
	query := "UPDATE bookmarks_userprofile SET " + strings.Join(columns, ",") + " WHERE user_id = " + marker(engine, len(values))
	result, err := executor.ExecContext(ctx, query, values...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateProfileWithTasks(ctx context.Context, db *sql.DB, engine string, userID int64, form url.Values, backgroundDisabled bool) error {
	before, err := LoadProfileForm(ctx, db, engine, userID)
	if err != nil {
		return err
	}
	if err := UpdateProfile(ctx, db, engine, userID, form); err != nil {
		return err
	}
	if backgroundDisabled {
		return nil
	}
	newFavicons := before.Get("enable_favicons") == "" && form.Get("enable_favicons") != ""
	newPreviews := before.Get("enable_preview_images") == "" && form.Get("enable_preview_images") != ""
	return jobs.EnqueueMissingMedia(ctx, db, engine, userID, newFavicons, newPreviews)
}
