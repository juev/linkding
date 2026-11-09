package httpserver

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// versionedAssets are the static files that pages load with ?v=<version>.
// Only these get a long-lived Cache-Control, because only their content
// feeds the version.
var versionedAssets = []string{"bundle.js", "theme-light.css", "theme-dark.css", "icons.svg"}

// staticCacheForever is sent for a versioned asset requested with the
// current version: a new build changes the URL, so the copy never goes stale.
const staticCacheForever = "public, max-age=31536000, immutable"

// assetVersioner derives the cache-busting version from the content of the
// versioned assets. It rehashes only when a file's size or modification time
// changes, so a rebuilt bundle gets a new URL without a server restart.
type assetVersioner struct {
	dir     string
	mu      sync.Mutex
	stamp   string
	version string
}

func newAssetVersioner(dir string) *assetVersioner {
	return &assetVersioner{dir: dir}
}

func (a *assetVersioner) Version() string {
	var stamp strings.Builder
	for _, name := range versionedAssets {
		stamp.WriteString(name)
		if info, err := os.Stat(filepath.Join(a.dir, name)); err == nil {
			stamp.WriteString(":" + strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10))
		}
		stamp.WriteByte(';')
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.version != "" && a.stamp == stamp.String() {
		return a.version
	}
	digest := sha256.New()
	for _, name := range versionedAssets {
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		if data, err := os.ReadFile(filepath.Join(a.dir, name)); err == nil {
			digest.Write(data)
		}
		digest.Write([]byte{0})
	}
	a.stamp = stamp.String()
	a.version = hex.EncodeToString(digest.Sum(nil))[:12]
	return a.version
}

// isVersionedAsset reports whether a /static/ path is one of the versioned files.
func isVersionedAsset(name string) bool {
	for _, asset := range versionedAssets {
		if name == asset {
			return true
		}
	}
	return false
}

// activeAssets backs the assetVersion template function. Templates are parsed
// once at package initialization, so the server built by New publishes its
// versioner here; a process runs one server.
var activeAssets atomic.Pointer[assetVersioner]

func currentAssetVersion() string {
	if versioner := activeAssets.Load(); versioner != nil {
		return versioner.Version()
	}
	return ""
}

var assetTemplateFuncs = template.FuncMap{"assetVersion": currentAssetVersion}

// parsePageTemplate parses embedded page templates with the asset functions.
func parsePageTemplate(files embed.FS, patterns ...string) *template.Template {
	return template.Must(template.New(path.Base(patterns[0])).Funcs(assetTemplateFuncs).ParseFS(files, patterns...))
}

// parsePageString parses an inline page template with the asset functions.
func parsePageString(name, text string) *template.Template {
	return template.Must(template.New(name).Funcs(assetTemplateFuncs).Parse(text))
}
