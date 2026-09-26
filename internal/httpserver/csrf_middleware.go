package httpserver

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/config"
)

func csrfMiddleware(next http.Handler, mux *http.ServeMux, cfg config.Config) http.Handler {
	prefix := cfg.URLPrefix()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && r.Method != http.MethodTrace &&
			csrfProtectedUIPath(mux, r, prefix) {
			if r.Header.Get("X-CSRFToken") == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				limit := cfg.RequestMaxContentLength
				if limit <= 0 {
					limit = 100 << 20
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
				if err := r.ParseMultipartForm(32 << 20); err != nil {
					writeCSRFFailure(w, r)
					return
				}
			}
			if !verifyAPICSRF(r, cfg) {
				writeCSRFFailure(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func csrfProtectedUIPath(mux *http.ServeMux, r *http.Request, prefix string) bool {
	path := r.URL.Path
	if strings.HasPrefix(path, prefix+"api") || strings.HasPrefix(path, prefix+"static/") {
		return false
	}
	_, pattern := mux.Handler(r)
	switch pattern {
	case "":
		return false
	case prefix:
		return path == prefix
	case prefix + "bookmarks/":
		return path == prefix+"bookmarks/new" || canonicalEditPath(path, prefix+"bookmarks/")
	case prefix + "tags/":
		return path == prefix+"tags/new" || path == prefix+"tags/merge" || canonicalEditPath(path, prefix+"tags/")
	case prefix + "bundles/":
		return path == prefix+"bundles/new" || path == prefix+"bundles/action" || path == prefix+"bundles/preview" || canonicalEditPath(path, prefix+"bundles/")
	case prefix + "assets/":
		part := strings.TrimPrefix(path, prefix+"assets/")
		segments := strings.Split(part, "/")
		if len(segments) > 2 || len(segments) == 2 && segments[1] != "read" {
			return false
		}
		id, err := strconv.ParseInt(segments[0], 10, 64)
		return err == nil && id > 0 && segments[0] == strconv.FormatInt(id, 10)
	case prefix + "feeds/":
		part := strings.TrimPrefix(path, prefix+"feeds/")
		if part == "shared" {
			return true
		}
		segments := strings.Split(part, "/")
		return len(segments) == 2 && segments[0] != "" && (segments[1] == "all" || segments[1] == "unread" || segments[1] == "shared")
	case prefix + "admin/":
		return true
	case prefix + "oidc/":
		return path == prefix+"oidc/authenticate/" || path == prefix+"oidc/callback/" || path == prefix+"oidc/logout/"
	default:
		return path == pattern
	}
}

func canonicalEditPath(path, root string) bool {
	if !strings.HasPrefix(path, root) || !strings.HasSuffix(path, "/edit") {
		return false
	}
	part := strings.TrimSuffix(strings.TrimPrefix(path, root), "/edit")
	id, err := strconv.ParseInt(part, 10, 64)
	return err == nil && id > 0 && part == strconv.FormatInt(id, 10)
}
