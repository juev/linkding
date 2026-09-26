package httpserver

import (
	"embed"
	"encoding/binary"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// These compiled Django 6.0.7 catalogs are copied from the pinned v1.47.0
// runtime. Their BSD-3-Clause notice is in docs/third-party/django-LICENSE.
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
		for _, domain := range []string{"core", "auth", "admin"} {
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
