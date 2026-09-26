package httpserver

import (
	"embed"
	"encoding/binary"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// These compiled Django 6.0.7 and DRF 3.17.2 catalogs are copied from the
// pinned v1.47.0 runtime. Their BSD-3-Clause notices are in docs/third-party.
//
//go:embed admin_locale/*/*.mo
var adminLocaleFiles embed.FS

type adminLanguage struct {
	Code string
	Dir  string
}

var adminCatalogs sync.Map
var adminLanguageCodes struct {
	sync.Once
	values map[string]bool
}

func selectedAdminLanguage(r *http.Request) adminLanguage {
	if cookie, err := r.Cookie("ld_language"); err == nil {
		if code, ok := supportedAdminLanguage(cookie.Value); ok {
			return languageWithDirection(code)
		}
	}
	type preference struct {
		code   string
		weight float64
	}
	var preferences []preference
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		pieces := strings.Split(strings.TrimSpace(part), ";")
		if len(pieces) == 0 || pieces[0] == "" {
			continue
		}
		weight := 1.0
		for _, option := range pieces[1:] {
			option = strings.TrimSpace(option)
			if strings.HasPrefix(option, "q=") {
				parsed, err := strconv.ParseFloat(strings.TrimPrefix(option, "q="), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					weight = 0
				} else {
					weight = parsed
				}
			}
		}
		if weight > 0 {
			preferences = append(preferences, preference{pieces[0], weight})
		}
	}
	sort.SliceStable(preferences, func(i, j int) bool { return preferences[i].weight > preferences[j].weight })
	for _, choice := range preferences {
		if code, ok := supportedAdminLanguage(choice.code); ok {
			return languageWithDirection(code)
		}
	}
	return adminLanguage{Code: "en", Dir: "ltr"}
}

func languageWithDirection(code string) adminLanguage {
	direction := "ltr"
	switch strings.Split(code, "-")[0] {
	case "ar", "fa", "he", "ps", "sd", "ug", "ur", "yi":
		direction = "rtl"
	}
	return adminLanguage{Code: code, Dir: direction}
}

func supportedAdminLanguage(value string) (string, bool) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	if value == "en" || strings.HasPrefix(value, "en-") {
		return "en", true
	}
	adminLanguageCodes.Do(func() {
		adminLanguageCodes.values = make(map[string]bool)
		files, _ := fs.ReadDir(adminLocaleFiles, "admin_locale/admin")
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".mo") {
				code := strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(file.Name(), ".mo"), "_", "-"))
				adminLanguageCodes.values[code] = true
			}
		}
	})
	if adminLanguageCodes.values[value] {
		return value, true
	}
	base := strings.Split(value, "-")[0]
	if adminLanguageCodes.values[base] {
		return base, true
	}
	return "", false
}

func adminTranslate(language, key string) string {
	if language == "en" {
		return key
	}
	value, ok := adminCatalogs.Load(language)
	if !ok {
		catalog := make(map[string]string)
		fileCode := strings.ReplaceAll(language, "-", "_")
		for _, domain := range []string{"core", "contenttypes", "sessions", "rest_framework", "auth", "admin"} {
			data, err := adminLocaleFiles.ReadFile("admin_locale/" + domain + "/" + fileCode + ".mo")
			if err != nil {
				continue
			}
			entries, err := parseAdminMO(data)
			if err != nil {
				continue
			}
			for original, translated := range entries {
				if translated != "" {
					catalog[original] = translated
				}
			}
		}
		value, _ = adminCatalogs.LoadOrStore(language, catalog)
	}
	if translated, ok := value.(map[string]string)[key]; ok {
		return strings.Split(translated, "\x00")[0]
	}
	return key
}

func parseAdminMO(data []byte) (map[string]string, error) {
	if len(data) < 28 {
		return nil, errors.New("short gettext catalog")
	}
	var order binary.ByteOrder
	switch binary.LittleEndian.Uint32(data[:4]) {
	case 0x950412de:
		order = binary.LittleEndian
	case 0xde120495:
		order = binary.BigEndian
	default:
		return nil, errors.New("invalid gettext catalog magic")
	}
	count := uint64(order.Uint32(data[8:12]))
	originals := uint64(order.Uint32(data[12:16]))
	translations := uint64(order.Uint32(data[16:20]))
	if count > uint64(len(data))/8 || originals+count*8 > uint64(len(data)) || translations+count*8 > uint64(len(data)) {
		return nil, errors.New("invalid gettext string table")
	}
	entries := make(map[string]string, int(count))
	for i := uint64(0); i < count; i++ {
		originalSize := uint64(order.Uint32(data[originals+i*8 : originals+i*8+4]))
		originalOffset := uint64(order.Uint32(data[originals+i*8+4 : originals+i*8+8]))
		translationSize := uint64(order.Uint32(data[translations+i*8 : translations+i*8+4]))
		translationOffset := uint64(order.Uint32(data[translations+i*8+4 : translations+i*8+8]))
		if originalOffset+originalSize > uint64(len(data)) || translationOffset+translationSize > uint64(len(data)) {
			return nil, errors.New("invalid gettext string offset")
		}
		original := string(data[originalOffset : originalOffset+originalSize])
		if original != "" {
			entries[original] = string(data[translationOffset : translationOffset+translationSize])
		}
	}
	return entries, nil
}

func adminCapitalized(value string) string {
	first, size := utf8.DecodeRuneInString(value)
	if size == 0 {
		return value
	}
	return string(unicode.ToUpper(first)) + value[size:]
}

func adminAppTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Models in the %(name)s application"), "%(name)s", name)
}

func adminAppIndexTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "%(app)s administration"), "%(app)s", name)
}

func adminSelectTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Select %s to change"), "%s", name)
}

func adminAddTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Add %(name)s"), "%(name)s", name)
}

func adminSearchTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Search %(name)s"), "%(name)s", name)
}

func adminActionCounter(language string, count int) string {
	return strings.ReplaceAll(adminTranslate(language, "0 of %(cnt)s selected"), "%(cnt)s", strconv.Itoa(count))
}

func adminDeleteSelected(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Delete selected %(verbose_name_plural)s"), "%(verbose_name_plural)s", name)
}

func adminPaginationTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Pagination %(name)s"), "%(name)s", name)
}

func adminRowActionAria(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Select this object for an action - {}"), "{}", name)
}

func adminFilterTitle(language, title string) string {
	if language != "en" && strings.HasPrefix(title, "By ") {
		field := strings.ToLower(strings.TrimPrefix(title, "By "))
		if translated := adminTranslate(language, field); translated != field {
			return translated
		}
	}
	return adminTranslate(language, title)
}

func adminFormTitle(language, title string) string {
	for _, prefix := range []string{"Change history: ", "Change password: "} {
		if name, ok := strings.CutPrefix(title, prefix); ok {
			return strings.ReplaceAll(adminTranslate(language, prefix+"%s"), "%s", name)
		}
	}
	if strings.HasPrefix(title, "Delete ") {
		return adminTranslate(language, "Delete")
	}
	for _, verb := range []string{"Add", "Change", "View", "Delete"} {
		if name, ok := strings.CutPrefix(title, verb+" "); ok {
			if name == "user" {
				name = adminTranslate(language, name)
			}
			translated := adminTranslate(language, verb+" %s")
			if translated == verb+" %s" {
				translated = adminTranslate(language, verb) + " %s"
			}
			return strings.ReplaceAll(translated, "%s", name)
		}
	}
	return adminTranslate(language, title)
}

func adminHistoryTitle(language, name string) string {
	return strings.ReplaceAll(adminTranslate(language, "Change history: %s"), "%s", name)
}

func adminPasswordMinimum(language string) string {
	if language == "ru" {
		return "Ваш пароль должен содержать как минимум 8 символов."
	}
	return "Your password must contain at least 8 characters."
}

func adminPasswordPrompt(language, username string) template.HTML {
	message := adminTranslate(language, "Enter a new password for the user <strong>%(username)s</strong>.")
	return template.HTML(strings.ReplaceAll(message, "%(username)s", template.HTMLEscapeString(username)))
}

func adminPasswordEnableMessage(language string) template.HTML {
	return template.HTML(adminTranslate(language, "This action will <strong>enable</strong> password-based authentication for this user."))
}

func adminRelatedTitle(language, action, model string) string {
	key := action + " selected %(model)s"
	if action == "Add another" {
		key = "Add another %(model)s"
	}
	if language == "en" {
		return strings.ReplaceAll(key, "%(model)s", model)
	}
	translated := adminTranslate(language, key)
	translated = strings.ReplaceAll(translated, `"%(model)s"`, "")
	return strings.ReplaceAll(translated, "%(model)s", model)
}

func adminCapTranslate(language, key string) string {
	return adminCapitalized(adminTranslate(language, key))
}

func adminPermissionLabel(language, label string) string {
	parts := strings.Split(label, " | ")
	if len(parts) != 3 {
		return label
	}
	app, model := adminTranslate(language, parts[0]), adminTranslate(language, parts[1])
	if language == "ru" {
		switch parts[0] {
		case "Auth Token":
			app = "Токен аутентификации"
		case "Sessions":
			app = "Сессии"
		}
		switch parts[1] {
		case "Token":
			model = "Токен"
		case "session":
			model = "сессия"
		}
	}
	return app + " | " + model + " | " + parts[2]
}

func adminSingleDeletePrompt(language, model, repr string) string {
	message := adminTranslate(language, "Are you sure you want to delete the %(object_name)s “%(escaped_object)s”? All of the following related items will be deleted:")
	message = strings.ReplaceAll(message, "%(object_name)s", model)
	return strings.ReplaceAll(message, "%(escaped_object)s", repr)
}

func adminDeletionLabel(language, label string) string {
	switch label {
	case "User":
		return adminCapTranslate(language, "user")
	case "Users":
		return adminCapTranslate(language, "users")
	case "Log entry":
		return adminCapTranslate(language, "log entry")
	case "Log entries":
		return adminCapTranslate(language, "log entries")
	default:
		return label
	}
}

func adminDeletionLogRepr(language, repr string) string {
	for _, verb := range []string{"Added", "Changed", "Deleted"} {
		if object, ok := strings.CutPrefix(repr, verb+" “"); ok && strings.HasSuffix(object, "”.") {
			key := verb + " “%(object)s”."
			return strings.ReplaceAll(adminTranslate(language, key), "%(object)s", strings.TrimSuffix(object, "”."))
		}
	}
	return repr
}

func localizeAdminDeletionGraph(language string, summary []adminDeletionSummary, nodes []adminDeletionNode) {
	for i := range summary {
		summary[i].Label = adminDeletionLabel(language, summary[i].Label)
	}
	var localizeNodes func([]adminDeletionNode)
	localizeNodes = func(items []adminDeletionNode) {
		for i := range items {
			if items[i].Label == "Log entry" {
				items[i].Repr = adminDeletionLogRepr(language, items[i].Repr)
			}
			items[i].Label = adminDeletionLabel(language, items[i].Label)
			localizeNodes(items[i].Children)
		}
	}
	localizeNodes(nodes)
}
