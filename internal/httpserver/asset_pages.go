package httpserver

import (
	"compress/gzip"
	"database/sql"
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

//go:embed reader.html
var readerTemplateFile embed.FS

var readerTemplate = template.Must(template.ParseFS(readerTemplateFile, "reader.html"))

type readerData struct {
	Prefix       string
	Theme        string
	CustomCSSURL template.URL
	Content      template.HTML
}

func serveAssetPage(w http.ResponseWriter, r *http.Request, prefix string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeNotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(path, "/")
	if len(parts) > 2 || len(parts) == 2 && parts[1] != "read" {
		writeNotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		writeNotFound(w, r)
		return
	}
	var user auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		user, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	query := `SELECT id, bookmark_id, date_created, file_size, asset_type, content_type,
		display_name, status, file, gzip FROM bookmarks_bookmarkasset WHERE id = ` + assetMarker(cfg.DBEngine, 1)
	asset, err := scanAsset(db.QueryRowContext(r.Context(), query, id))
	if err != nil {
		writeNotFound(w, r)
		return
	}
	query = `SELECT b.owner_id, b.shared, p.enable_sharing, p.enable_public_sharing
		FROM bookmarks_bookmark b JOIN bookmarks_userprofile p ON p.user_id = b.owner_id
		WHERE b.id = ` + assetMarker(cfg.DBEngine, 1)
	var ownerID int64
	var shared, sharing, publicSharing bool
	if err := db.QueryRowContext(r.Context(), query, asset.Bookmark).Scan(&ownerID, &shared, &sharing, &publicSharing); err != nil ||
		!(user.ID != 0 && user.ID == ownerID || user.ID != 0 && shared && sharing || shared && publicSharing) {
		writeNotFound(w, r)
		return
	}
	if len(parts) == 1 {
		serveAssetFile(w, r, cfg, asset, true)
		return
	}
	file, err := openAssetPageFile(cfg, asset)
	if err != nil {
		writeNotFound(w, r)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	data := readerData{Prefix: cfg.URLPrefix(), Theme: "auto", Content: template.HTML(content)}
	profileID := user.ID
	if profileID == 0 {
		var guest sql.NullInt64
		err := db.QueryRowContext(r.Context(), "SELECT guest_profile_user_id FROM bookmarks_globalsettings ORDER BY id LIMIT 1").Scan(&guest)
		if err == nil && guest.Valid {
			profileID = guest.Int64
		}
	}
	if profileID != 0 {
		query := "SELECT theme, custom_css FROM bookmarks_userprofile WHERE user_id = " + assetMarker(cfg.DBEngine, 1)
		var css string
		if err := db.QueryRowContext(r.Context(), query, profileID).Scan(&data.Theme, &css); err == nil && css != "" {
			data.CustomCSSURL = template.URL("data:text/css;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(css)))
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	if r.Method == http.MethodHead {
		return
	}
	if err := readerTemplate.Execute(w, data); err != nil {
		return
	}
}

func openAssetPageFile(cfg config.Config, asset apiAsset) (io.ReadCloser, error) {
	if asset.file == "" {
		return nil, os.ErrNotExist
	}
	root, err := os.OpenRoot(filepath.Join(cfg.DataDir, "assets"))
	if err != nil {
		return nil, err
	}
	file, err := root.Open(asset.file)
	root.Close()
	if err != nil {
		return nil, err
	}
	if !asset.compressed {
		return file, nil
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &assetGzipReadCloser{Reader: reader, file: file}, nil
}

type assetGzipReadCloser struct {
	*gzip.Reader
	file *os.File
}

func (reader *assetGzipReadCloser) Close() error {
	return errors.Join(reader.Reader.Close(), reader.file.Close())
}
