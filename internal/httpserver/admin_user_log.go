package httpserver

import (
	"maps"
	"strconv"
	"strings"
)

func adminUserChangedFields(before, after adminUserData) []string {
	var changed []string
	for _, field := range []struct {
		label string
		dirty bool
	}{
		{"Username", before.UserName != after.UserName},
		{"First name", before.FirstName != after.FirstName},
		{"Last name", before.LastName != after.LastName},
		{"Email address", before.Email != after.Email},
		{"Active", before.IsActive != after.IsActive},
		{"Staff status", before.IsStaff != after.IsStaff},
		{"Superuser status", before.IsSuperuser != after.IsSuperuser},
		{"Groups", !maps.Equal(before.GroupIDs, after.GroupIDs)},
		{"User permissions", !maps.Equal(before.PermissionIDs, after.PermissionIDs)},
		{"Last login", before.LastLoginDate != after.LastLoginDate || before.LastLoginTime != after.LastLoginTime},
		{"Date joined", before.DateJoinedDate != after.DateJoinedDate || before.DateJoinedTime != after.DateJoinedTime},
	} {
		if field.dirty {
			changed = append(changed, field.label)
		}
	}
	return changed
}

func adminUserChangeMessage(before, after adminUserData) string {
	parent := adminChangeMessage(adminUserChangedFields(before, after))
	var profileFields []string
	for _, field := range []struct{ name, label string }{
		{"theme", "Theme"}, {"bookmark_date_display", "Bookmark date display"},
		{"bookmark_description_display", "Bookmark description display"},
		{"bookmark_description_max_lines", "Bookmark description max lines"},
		{"bookmark_link_target", "Bookmark link target"}, {"web_archive_integration", "Web archive integration"},
		{"tag_search", "Tag search"}, {"tag_grouping", "Tag grouping"},
		{"enable_sharing", "Enable sharing"}, {"enable_public_sharing", "Enable public sharing"},
		{"enable_favicons", "Enable favicons"}, {"enable_preview_images", "Enable preview images"},
		{"display_url", "Display url"}, {"display_view_bookmark_action", "Display view bookmark action"},
		{"display_edit_bookmark_action", "Display edit bookmark action"},
		{"display_archive_bookmark_action", "Display archive bookmark action"},
		{"display_remove_bookmark_action", "Display remove bookmark action"},
		{"permanent_notes", "Permanent notes"}, {"custom_css", "Custom css"},
		{"auto_tagging_rules", "Auto tagging rules"},
		{"enable_automatic_html_snapshots", "Enable automatic html snapshots"},
		{"default_mark_unread", "Default mark unread"}, {"default_mark_shared", "Default mark shared"},
		{"items_per_page", "Items per page"}, {"sticky_pagination", "Sticky pagination"},
		{"collapse_side_panel", "Collapse side panel"}, {"hide_bundles", "Hide bundles"},
		{"legacy_search", "Legacy search"},
	} {
		if before.Profile.Form.Get(field.name) != after.Profile.Form.Get(field.name) {
			profileFields = append(profileFields, field.label)
		}
		if field.name == "custom_css" && before.Profile.CustomCSSHash != after.Profile.CustomCSSHash {
			profileFields = append(profileFields, "Custom css hash")
		}
	}
	if len(profileFields) == 0 {
		return parent
	}
	inline := `{"changed": {"name": "user profile", "object": "UserProfile object (` + strconv.FormatInt(after.Profile.ID, 10) + `)", "fields": ` + adminJSONList(profileFields) + `}}`
	if parent == "[]" {
		return "[" + inline + "]"
	}
	return strings.TrimSuffix(parent, "]") + ", " + inline + "]"
}
