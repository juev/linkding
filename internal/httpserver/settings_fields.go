package httpserver

import (
	"html/template"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/settings"
)

var profileFieldOrder = []string{
	"theme", "bookmark_date_display", "bookmark_description_display", "bookmark_description_max_lines",
	"display_url", "permanent_notes", "display_view_bookmark_action", "display_edit_bookmark_action",
	"display_archive_bookmark_action", "display_remove_bookmark_action", "bookmark_link_target", "items_per_page",
	"sticky_pagination", "collapse_side_panel", "hide_bundles", "tag_search", "legacy_search",
	"tag_grouping", "auto_tagging_rules", "enable_favicons", "enable_preview_images",
	"web_archive_integration", "enable_sharing", "enable_public_sharing",
	"enable_automatic_html_snapshots", "default_mark_unread", "default_mark_shared", "custom_css",
}

var profileLabels = map[string]string{
	"theme": "Theme", "bookmark_date_display": "Bookmark date format",
	"bookmark_description_display": "Bookmark description", "bookmark_description_max_lines": "Bookmark description max lines",
	"display_url": "Show bookmark URL", "permanent_notes": "Show notes permanently",
	"display_view_bookmark_action": "View", "display_edit_bookmark_action": "Edit",
	"display_archive_bookmark_action": "Archive", "display_remove_bookmark_action": "Remove",
	"bookmark_link_target": "Open bookmarks in", "items_per_page": "Items per page",
	"sticky_pagination": "Sticky pagination", "collapse_side_panel": "Collapse side panel",
	"hide_bundles": "Hide bundles", "tag_search": "Tag search", "legacy_search": "Enable legacy search",
	"tag_grouping": "Tag grouping", "auto_tagging_rules": "Auto Tagging",
	"enable_favicons": "Enable Favicons", "enable_preview_images": "Enable Preview Images",
	"web_archive_integration": "Internet Archive integration", "enable_sharing": "Enable bookmark sharing",
	"enable_public_sharing":           "Enable public bookmark sharing",
	"enable_automatic_html_snapshots": "Automatically create HTML snapshots",
	"default_mark_unread":             "Create bookmarks as unread by default",
	"default_mark_shared":             "Create bookmarks as shared by default", "custom_css": "Custom CSS",
}

// These fragments are fixed template content, never user input.
var profileHelp = map[string]template.HTML{
	"theme":                           "Whether to use a light or dark theme, or automatically adjust the theme based on your system's settings.",
	"bookmark_date_display":           "Whether to show bookmark dates as relative (how long ago), or as absolute dates. Alternatively the date can be hidden.",
	"bookmark_description_display":    "Whether to show bookmark descriptions and tags in the same line, or as separate blocks.",
	"bookmark_description_max_lines":  "Limits the number of lines that are displayed for the bookmark description.",
	"display_url":                     "When enabled, this setting displays the bookmark URL below the title.",
	"permanent_notes":                 "Whether to show bookmark notes permanently, without having to toggle them individually. Alternatively the keyboard shortcut <code>e</code> can be used to temporarily show all notes.",
	"bookmark_link_target":            "Whether to open bookmarks a new page or in the same page.",
	"items_per_page":                  "The number of bookmarks to display per page.",
	"sticky_pagination":               "When enabled, the pagination controls will stick to the bottom of the screen, so that they are always visible without having to scroll to the end of the page first.",
	"collapse_side_panel":             "When enabled, the tags side panel will be collapsed by default to give more space to the bookmark list. Instead, the tags are shown in an expandable drawer.",
	"hide_bundles":                    "Allows to hide the bundles in the side panel if you don't intend to use them.",
	"tag_search":                      "In strict mode, tags must be prefixed with a hash character (#). In lax mode, tags can also be searched without the hash character. Note that tags without the hash character are indistinguishable from search terms, which means the search result will also include bookmarks where a search term matches otherwise.",
	"legacy_search":                   "Since version 1.44.0, linkding has a new search engine that supports logical expressions (and, or, not). If you run into any issues with the new search, you can enable this option to temporarily switch back to the old search. Please report any issues you encounter with the new search on <a href=\"https://github.com/sissbruecker/linkding/issues\" target=\"_blank\">GitHub</a> so they can be addressed. This option will be removed in a future version.",
	"tag_grouping":                    "In alphabetical mode, tags will be grouped by the first letter. If disabled, tags will not be grouped.",
	"auto_tagging_rules":              "Automatically adds tags to bookmarks based on predefined rules. Each line is a single rule that maps a URL to one or more tags. For example: <pre>youtube.com video\nreddit.com/r/Music music reddit</pre>",
	"enable_favicons":                 "Automatically loads favicons for bookmarked websites and displays them next to each bookmark. Enabling this feature automatically downloads all missing favicons. By default, this feature uses a <b>Google service</b> to download favicons. If you don't want to use this service, check the <a href=\"https://linkding.link/options/#ld_favicon_provider\" target=\"_blank\">options documentation</a> on how to configure a custom favicon provider. Icons are downloaded in the background, and it may take a while for them to show up.",
	"enable_preview_images":           "Automatically loads preview images for bookmarked websites and displays them next to each bookmark. Enabling this feature automatically downloads all missing preview images.",
	"web_archive_integration":         "Enabling this feature will automatically create snapshots of bookmarked websites on the <a href=\"https://web.archive.org/\" target=\"_blank\" rel=\"noopener\">Internet Archive Wayback Machine</a>. This allows to preserve, and later access the website as it was at the point in time it was bookmarked, in case it goes offline or its content is modified. Please consider donating to the <a href=\"https://archive.org/donate\" target=\"_blank\" rel=\"noopener\">Internet Archive</a> if you make use of this feature.",
	"enable_sharing":                  "Allows to share bookmarks with other users, and to view shared bookmarks. Disabling this feature will hide all previously shared bookmarks from other users.",
	"enable_public_sharing":           "Makes shared bookmarks publicly accessible, without requiring a login. That means that anyone with a link to this instance can view shared bookmarks via the",
	"enable_automatic_html_snapshots": "Automatically creates HTML snapshots when adding bookmarks. Alternatively, when disabled, snapshots can be created manually in the details view of a bookmark.",
	"default_mark_unread":             "Sets the default state for the \"Mark as unread\" option when creating a new bookmark. Setting this option will make all new bookmarks default to unread. This can be overridden when creating each new bookmark.",
	"default_mark_shared":             "Sets the default state for the \"Share\" option when creating a new bookmark. Setting this option will make all new bookmarks default to shared. This can be overridden when creating each new bookmark.",
	"custom_css":                      "Allows to add custom CSS to the page.",
}

func profileDisplayFields(form url.Values) []settingsField {
	byName := make(map[string]settings.ProfileField, len(settings.ProfileFields))
	for _, field := range settings.ProfileFields {
		byName[field.Name] = field
	}
	result := make([]settingsField, 0, len(profileFieldOrder))
	for _, name := range profileFieldOrder {
		definition := byName[name]
		value := form.Get(name)
		field := settingsField{
			Name: name, Label: profileLabels[name], Kind: definition.Kind, Value: value, Help: profileHelp[name],
			Checked: value != "" && value != "0" && !strings.EqualFold(value, "false"),
			Hidden:  name == "bookmark_description_max_lines" && form.Get("bookmark_description_display") == "inline",
		}
		if definition.Kind == "choice" || definition.Kind == "number" {
			field.Class = "width-25 width-sm-100"
		}
		if definition.Kind == "text" {
			field.Class = "monospace"
		}
		for _, option := range definition.Choices {
			field.Options = append(field.Options, settingsOption{Value: option, Label: choiceLabel(name, option), Selected: value == option})
		}
		result = append(result, field)
	}
	return result
}

func choiceLabel(name, value string) string {
	labels := map[string]map[string]string{
		"bookmark_link_target":         {"_blank": "New page", "_self": "Same page"},
		"bookmark_date_display":        {"relative": "Relative", "absolute": "Absolute", "hidden": "Hidden"},
		"bookmark_description_display": {"inline": "Inline", "separate": "Separate"},
		"tag_grouping":                 {"alphabetical": "Alphabetical", "disabled": "Disabled"},
	}
	if label := labels[name][value]; label != "" {
		return label
	}
	if value == "_blank" {
		return "New page"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
