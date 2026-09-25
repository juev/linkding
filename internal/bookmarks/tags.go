package bookmarks

import (
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ParseTagString follows the upstream Tag model helpers: trim surrounding
// spaces, replace inner ASCII spaces, keep the last spelling of duplicates,
// then sort case-insensitively.
func ParseTagString(value, delimiter string) []string {
	if value == "" {
		return nil
	}
	if delimiter == "" {
		delimiter = ","
	}
	names := strings.Split(strings.TrimSpace(value), delimiter)
	lower := cases.Lower(language.Und)
	order := make([]string, 0, len(names))
	last := make(map[string]string, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		name = strings.ReplaceAll(strings.TrimSpace(name), " ", "-")
		key := lower.String(name)
		if _, exists := last[key]; !exists {
			order = append(order, key)
		}
		last[key] = name
	}
	result := make([]string, len(order))
	for i, key := range order {
		result[i] = last[key]
	}
	sort.SliceStable(result, func(i, j int) bool { return lower.String(result[i]) < lower.String(result[j]) })
	return result
}
