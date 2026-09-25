package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func serveStaticFile(w http.ResponseWriter, r *http.Request, prefix, staticDir, dataDir string) {
	w.Header().Set("Content-Security-Policy", "sandbox")
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || !filepath.IsLocal(name) {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(filepath.Join(staticDir, name)); err == nil && info.Mode().IsRegular() {
		http.StripPrefix(prefix, http.FileServer(http.Dir(staticDir))).ServeHTTP(w, r)
		return
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || dataDir == "" {
		http.NotFound(w, r)
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
		http.ServeContent(w, r, name, info.ModTime(), file)
		file.Close()
		return
	}
	http.NotFound(w, r)
}
