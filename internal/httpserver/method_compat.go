package httpserver

import (
	"net/http"
	"strings"
)

// Django function views render their GET response for OPTIONS, while its form
// views also accept HEAD. Keep this translation outside the REST API.
func uiReadMethodCompatibility(next http.Handler, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if r.Method == http.MethodOptions && uiOptionsRendersPage(path) ||
			r.Method == http.MethodHead && uiFormPage(path) {
			request := r.Clone(r.Context())
			request.Method = http.MethodGet
			next.ServeHTTP(w, request)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func uiOptionsRendersPage(path string) bool {
	switch path {
	case "bookmarks", "bookmarks/archived", "bookmarks/shared", "bookmarks/close",
		"bundles", "bundles/new", "bundles/preview", "tags", "tags/new", "tags/merge",
		"settings", "settings/general", "settings/integrations", "admin/", "admin/tasks/", "health":
		return true
	}
	if strings.HasPrefix(path, "assets/") || strings.HasPrefix(path, "feeds/") {
		return true
	}
	if strings.HasPrefix(path, "admin/") &&
		!strings.HasPrefix(path, "admin/login/") &&
		!strings.HasPrefix(path, "admin/logout/") &&
		!strings.HasPrefix(path, "admin/password_change/") {
		return true
	}
	return uiFormPage(path)
}

func uiFormPage(path string) bool {
	switch path {
	case "bookmarks/new", "bundles/new", "bundles/preview", "tags/new", "tags/merge":
		return true
	}
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[1] != "" && parts[2] == "edit" &&
		(parts[0] == "bookmarks" || parts[0] == "bundles" || parts[0] == "tags")
}

func writeDjangoOptions(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}
