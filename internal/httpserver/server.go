package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/metadata"
)

const UpstreamVersion = "1.47.0"

// New builds the HTTP surface that is currently implemented. Additional routes
// must be added against the pinned upstream route matrix before release.
func New(db *sql.DB, cfg config.Config, staticDir string) http.Handler {
	mux := http.NewServeMux()
	prefix := cfg.URLPrefix()
	authRepo := auth.NewRepository(db, cfg.DBEngine)
	metadataCache := metadata.NewCache(10)
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		serveRoot(w, r, prefix, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		status := "healthy"
		code := http.StatusOK
		if err := db.PingContext(r.Context()); err != nil {
			status = "unhealthy"
			code = http.StatusInternalServerError
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Version string `json:"version"`
			Status  string `json:"status"`
		}{Version: UpstreamVersion, Status: status})
	})
	staticPrefix := prefix + "static/"
	mux.HandleFunc(staticPrefix, func(w http.ResponseWriter, r *http.Request) {
		serveStaticFile(w, r, staticPrefix, staticDir, cfg.DataDir)
	})
	assetPagePrefix := prefix + "assets/"
	mux.HandleFunc(assetPagePrefix, func(w http.ResponseWriter, r *http.Request) {
		serveAssetPage(w, r, assetPagePrefix, cfg, db, authRepo)
	})
	apiRoot := prefix + "api/"
	mux.HandleFunc(apiRoot, func(w http.ResponseWriter, r *http.Request) {
		serveAPIRoot(w, r, apiRoot, authRepo)
	})
	profilePath := prefix + "api/user/profile/"
	mux.HandleFunc(profilePath, func(w http.ResponseWriter, r *http.Request) {
		serveProfile(w, r, profilePath, authRepo)
	})
	bookmarkRoot := prefix + "api/bookmarks/"
	bookmarkRepo := bookmarks.NewRepositoryWithTasks(db, cfg.DBEngine, bookmarks.TaskPolicy{
		BackgroundDisabled: cfg.DisableBackgroundTasks, SnapshotsEnabled: cfg.EnableSnapshots,
	})
	bookmarkFormRoot := prefix + "bookmarks/"
	for _, listPath := range []string{prefix + "bookmarks", prefix + "bookmarks/archived", prefix + "bookmarks/shared"} {
		path := listPath
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			serveBookmarkList(w, r, path, cfg, db, authRepo, bookmarkRepo)
		})
	}
	for _, actionPath := range []string{prefix + "bookmarks/action", prefix + "bookmarks/archived/action", prefix + "bookmarks/shared/action"} {
		path := actionPath
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			serveBookmarkAction(w, r, path, cfg, db, authRepo, bookmarkRepo)
		})
	}
	mux.HandleFunc(bookmarkFormRoot, func(w http.ResponseWriter, r *http.Request) {
		serveBookmarkForm(w, r, bookmarkFormRoot, cfg, db, authRepo, bookmarkRepo)
	})
	bookmarkClosePath := prefix + "bookmarks/close"
	mux.HandleFunc(bookmarkClosePath, func(w http.ResponseWriter, r *http.Request) {
		serveBookmarkClose(w, r, bookmarkClosePath, cfg, authRepo, db)
	})
	mux.HandleFunc(bookmarkRoot, func(w http.ResponseWriter, r *http.Request) {
		serveBookmarksAPI(w, r, bookmarkRoot, cfg, db, authRepo, bookmarkRepo, metadataCache)
	})
	tagRoot := prefix + "api/tags/"
	mux.HandleFunc(tagRoot, func(w http.ResponseWriter, r *http.Request) {
		serveTagsAPI(w, r, tagRoot, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"tags", func(w http.ResponseWriter, r *http.Request) {
		serveTagsUI(w, r, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"tags/", func(w http.ResponseWriter, r *http.Request) {
		serveTagsUI(w, r, cfg, db, authRepo)
	})
	bundleRoot := prefix + "api/bundles/"
	mux.HandleFunc(bundleRoot, func(w http.ResponseWriter, r *http.Request) {
		serveBundlesAPI(w, r, bundleRoot, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"bundles", func(w http.ResponseWriter, r *http.Request) {
		serveBundlesUI(w, r, cfg, db, authRepo, bookmarkRepo)
	})
	mux.HandleFunc(prefix+"bundles/", func(w http.ResponseWriter, r *http.Request) {
		serveBundlesUI(w, r, cfg, db, authRepo, bookmarkRepo)
	})
	mux.HandleFunc(prefix+"toasts/acknowledge", func(w http.ResponseWriter, r *http.Request) {
		serveToastAcknowledge(w, r, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"admin/", func(w http.ResponseWriter, r *http.Request) {
		serveAdmin(w, r, cfg, db, authRepo)
	})
	feedRoot := prefix + "feeds/"
	mux.HandleFunc(feedRoot, func(w http.ResponseWriter, r *http.Request) {
		serveFeed(w, r, feedRoot, cfg, db, authRepo)
	})
	settingsPath := prefix + "settings/general"
	mux.HandleFunc(settingsPath, func(w http.ResponseWriter, r *http.Request) {
		serveSettingsGeneral(w, r, settingsPath, cfg, db, authRepo)
	})
	mux.HandleFunc(prefix+"settings", func(w http.ResponseWriter, r *http.Request) {
		serveSettingsGeneral(w, r, settingsPath, cfg, db, authRepo)
	})
	updatePath := prefix + "settings/update"
	mux.HandleFunc(updatePath, func(w http.ResponseWriter, r *http.Request) {
		serveSettingsUpdate(w, r, updatePath, cfg, db, authRepo)
	})
	integrationsPath := prefix + "settings/integrations"
	mux.HandleFunc(integrationsPath, func(w http.ResponseWriter, r *http.Request) {
		serveIntegrations(w, r, integrationsPath, cfg, db, authRepo)
	})
	createTokenPath := integrationsPath + "/create-api-token"
	mux.HandleFunc(createTokenPath, func(w http.ResponseWriter, r *http.Request) {
		serveCreateAPIToken(w, r, createTokenPath, cfg, db, authRepo)
	})
	deleteTokenPath := integrationsPath + "/delete-api-token"
	mux.HandleFunc(deleteTokenPath, func(w http.ResponseWriter, r *http.Request) {
		serveDeleteAPIToken(w, r, deleteTokenPath, cfg, db, authRepo)
	})
	importPath := prefix + "settings/import"
	mux.HandleFunc(importPath, func(w http.ResponseWriter, r *http.Request) {
		serveBookmarkImport(w, r, importPath, cfg, db, authRepo)
	})
	exportPath := prefix + "settings/export"
	mux.HandleFunc(exportPath, func(w http.ResponseWriter, r *http.Request) {
		serveBookmarkExport(w, r, exportPath, cfg, db, authRepo)
	})
	manifestPath := prefix + "manifest.json"
	mux.HandleFunc(manifestPath, func(w http.ResponseWriter, r *http.Request) {
		serveManifest(w, r, manifestPath, cfg, db, authRepo)
	})
	opensearchPath := prefix + "opensearch.xml"
	mux.HandleFunc(opensearchPath, func(w http.ResponseWriter, r *http.Request) {
		serveOpenSearch(w, r, opensearchPath, cfg)
	})
	customCSSPath := prefix + "custom_css"
	mux.HandleFunc(customCSSPath, func(w http.ResponseWriter, r *http.Request) {
		serveCustomCSS(w, r, customCSSPath, cfg, db, authRepo)
	})
	loginPath := prefix + "login/"
	mux.HandleFunc(loginPath, func(w http.ResponseWriter, r *http.Request) {
		serveLogin(w, r, loginPath, cfg, authRepo)
	})
	logoutPath := prefix + "logout/"
	mux.HandleFunc(logoutPath, func(w http.ResponseWriter, r *http.Request) {
		serveLogout(w, r, logoutPath, cfg, authRepo)
	})
	changePasswordPath := prefix + "change-password/"
	mux.HandleFunc(changePasswordPath, func(w http.ResponseWriter, r *http.Request) {
		serveChangePassword(w, r, changePasswordPath, cfg, db, authRepo)
	})
	passwordDonePath := prefix + "password-change-done/"
	mux.HandleFunc(passwordDonePath, func(w http.ResponseWriter, r *http.Request) {
		servePasswordChangeDone(w, r, passwordDonePath, cfg, db, authRepo)
	})
	if cfg.EnableOIDC {
		oidcRoot := prefix + "oidc/"
		mux.HandleFunc(oidcRoot, func(w http.ResponseWriter, r *http.Request) {
			serveOIDC(w, r, oidcRoot, cfg, db, authRepo)
		})
	}
	return corsMiddleware(authProxyMiddleware(uiReadMethodCompatibility(mux, prefix), cfg, authRepo), cfg)
}
