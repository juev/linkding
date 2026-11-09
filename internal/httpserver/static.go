package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// serveStaticFile serves built assets and, as a fallback, favicons and
// previews from the data directory. A versioned asset requested with the
// current version is cacheable for a year; every other response must be
// revalidated, because its URL does not change when its content does.
func serveStaticFile(w http.ResponseWriter, r *http.Request, prefix, staticDir, dataDir string, assets *assetVersioner) {
	w.Header().Set("Content-Security-Policy", "sandbox")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeNotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || !filepath.IsLocal(name) {
		writeNotFound(w, r)
		return
	}
	if info, err := os.Stat(filepath.Join(staticDir, name)); err == nil && info.Mode().IsRegular() {
		if isVersionedAsset(name) && r.URL.Query().Get("v") == assets.Version() {
			w.Header().Set("Cache-Control", staticCacheForever)
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.StripPrefix(prefix, http.FileServer(http.Dir(staticDir))).ServeHTTP(w, r)
		return
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || dataDir == "" {
		writeNotFound(w, r)
		return
	}
	for _, directory := range []string{"favicons", "previews"} {
		root, err := os.OpenRoot(filepath.Join(dataDir, directory))
		if err != nil {
			continue
		}
		file, err := root.Open(name)
		root.Close()
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			continue
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, name, info.ModTime(), file)
		file.Close()
		return
	}
	writeNotFound(w, r)
}
