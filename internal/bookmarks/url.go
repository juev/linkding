package bookmarks

import (
	"net/url"
	"sort"
	"strings"
)

// NormalizeURL mirrors bookmarks.utils.normalize_url from linkding v1.47.0.
// It is used for duplicate detection, not for rewriting the displayed URL.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	if u.User != nil {
		username := u.User.Username()
		if password, ok := u.User.Password(); ok && password != "" {
			username += ":" + password
		}
		host = username + "@" + host
	}
	path := u.EscapedPath()
	if u.Opaque != "" {
		path = u.Opaque
	}
	params := ""
	if lastSlash := strings.LastIndex(path, "/"); lastSlash >= 0 {
		if semi := strings.Index(path[lastSlash+1:], ";"); semi >= 0 {
			params = path[lastSlash+1+semi:]
			path = path[:lastSlash+1+semi]
		}
	}
	path = strings.TrimRight(path, "/") + params
	query := normalizeQuery(u.RawQuery)
	fragment := u.EscapedFragment()
	var result strings.Builder
	if scheme != "" {
		result.WriteString(scheme)
		result.WriteByte(':')
	}
	if host != "" {
		result.WriteString("//")
		result.WriteString(host)
		if path != "" && !strings.HasPrefix(path, "/") {
			result.WriteByte('/')
		}
	}
	result.WriteString(path)
	if query != "" {
		result.WriteByte('?')
		result.WriteString(query)
	}
	if fragment != "" {
		result.WriteByte('#')
		result.WriteString(fragment)
	}
	return result.String()
}

func normalizeQuery(raw string) string {
	type pair struct{ key, value string }
	var pairs []pair
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		decodedKey, errKey := url.QueryUnescape(key)
		decodedValue, errValue := url.QueryUnescape(value)
		if errKey != nil || errValue != nil {
			return raw
		}
		pairs = append(pairs, pair{decodedKey, decodedValue})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	encoded := make([]string, len(pairs))
	for i, p := range pairs {
		encoded[i] = pythonQuote(p.key) + "=" + pythonQuote(p.value)
	}
	return strings.Join(encoded, "&")
}

func pythonQuote(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
