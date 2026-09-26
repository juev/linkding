package httpserver

import (
	"net/http"
	"strings"
)

func canonicalAPISlashMiddleware(next http.Handler, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !canonicalAPISlashPath(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		location := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Location", location)
		w.Header().Set("Content-Length", "0")
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.WriteHeader(http.StatusMovedPermanently)
	})
}

func canonicalAPISlashPath(path, prefix string) bool {
	base := prefix + "api"
	if path == base {
		return true
	}
	if !strings.HasPrefix(path, base+"/") || strings.HasSuffix(path, "/") {
		return false
	}
	rest := strings.TrimPrefix(path, base+"/")
	switch rest {
	case "bookmarks", "tags", "bundles", "user/profile":
		return true
	}
	if strings.HasPrefix(rest, "tags/") || strings.HasPrefix(rest, "bundles/") {
		_, lookup, _ := strings.Cut(rest, "/")
		return validAPILookup(lookup)
	}
	if !strings.HasPrefix(rest, "bookmarks/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(rest, "bookmarks/"), "/")
	switch len(parts) {
	case 1:
		return validAPILookup(parts[0])
	case 2:
		return validAPILookup(parts[0]) && (parts[1] == "archive" || parts[1] == "unarchive" ||
			decimalAPIID(parts[0]) && parts[1] == "assets")
	case 3:
		return decimalAPIID(parts[0]) && parts[1] == "assets" && validAPILookup(parts[2])
	case 4:
		return decimalAPIID(parts[0]) && parts[1] == "assets" && validAPILookup(parts[2]) && parts[3] == "download"
	default:
		return false
	}
}

func validAPILookup(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/.")
}

func decimalAPIID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
