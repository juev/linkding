package bookmarks

import (
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

var trailingRuleComment = regexp.MustCompile(`\s+#`)

// AutoTags evaluates the v1.47.0 domain/path/query/fragment rule format.
func AutoTags(script, rawURL string) []string {
	actual, err := url.Parse(strings.ToLower(rawURL))
	if err != nil || actual.Hostname() == "" {
		return nil
	}
	actualHost, err := idna.Lookup.ToASCII(actual.Hostname())
	if err != nil {
		return nil
	}
	tags := make(map[string]struct{})
	for _, line := range strings.Split(strings.ToLower(script), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if comment := trailingRuleComment.FindStringIndex(line); comment != nil {
			line = line[:comment[0]]
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		patternText := strings.TrimPrefix(strings.TrimPrefix(parts[0], "https://"), "http://")
		pattern, err := url.Parse("//" + patternText)
		if err != nil || pattern.Hostname() == "" {
			continue
		}
		patternHost, err := idna.Lookup.ToASCII(pattern.Hostname())
		if err != nil || !strings.HasSuffix(actualHost, patternHost) {
			continue
		}
		if pattern.Path != "" && !strings.HasPrefix(actual.Path, pattern.Path) {
			continue
		}
		if pattern.RawQuery != "" && !queryMatches(pattern.RawQuery, actual.RawQuery) {
			continue
		}
		if pattern.Fragment != "" && !strings.HasPrefix(actual.Fragment, pattern.Fragment) {
			continue
		}
		for _, tag := range parts[1:] {
			tags[tag] = struct{}{}
		}
	}
	result := make([]string, 0, len(tags))
	for tag := range tags {
		result = append(result, tag)
	}
	slices.Sort(result)
	return result
}

func queryMatches(expected, actual string) bool {
	expectedValues, err := url.ParseQuery(expected)
	if err != nil {
		return false
	}
	actualValues, err := url.ParseQuery(actual)
	if err != nil {
		return false
	}
	for key, values := range expectedValues {
		actualForKey, ok := actualValues[key]
		if !ok {
			return false
		}
		for _, value := range values {
			if value == "" {
				continue
			}
			if !slices.Contains(actualForKey, value) {
				return false
			}
		}
	}
	return true
}
