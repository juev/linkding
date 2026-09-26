package httpserver

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// These error messages were captured from the pinned linkding v1.47.0 API.
//
//go:embed api_detail_translations.json
var apiDetailTranslationsJSON []byte

var apiDetailTranslations = func() map[string]map[string]string {
	var translations map[string]map[string]string
	if err := json.Unmarshal(apiDetailTranslationsJSON, &translations); err != nil {
		panic(err)
	}
	return translations
}()

func localizedAPIDetail(language, detail string) string {
	translations := apiDetailTranslations[language]
	if translated, ok := translations[detail]; ok {
		return translated
	}
	const methodPrefix = `Method "`
	const methodSuffix = `" not allowed.`
	if strings.HasPrefix(detail, methodPrefix) && strings.HasSuffix(detail, methodSuffix) {
		method := strings.TrimSuffix(strings.TrimPrefix(detail, methodPrefix), methodSuffix)
		if translated, ok := translations[`Method "POST" not allowed.`]; ok {
			return strings.ReplaceAll(translated, "POST", method)
		}
	}
	return detail
}
