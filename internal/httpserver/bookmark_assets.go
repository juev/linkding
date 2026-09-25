package httpserver

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

type apiAsset struct {
	ID          int64  `json:"id"`
	Bookmark    int64  `json:"bookmark"`
	DateCreated string `json:"date_created"`
	FileSize    *int64 `json:"file_size"`
	AssetType   string `json:"asset_type"`
	ContentType string `json:"content_type"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	file        string
	compressed  bool
}

func assetMarker(engine string, index int) string {
	if engine == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func scanAsset(row interface{ Scan(...any) error }) (apiAsset, error) {
	var asset apiAsset
	var date time.Time
	var size sql.NullInt64
	err := row.Scan(&asset.ID, &asset.Bookmark, &date, &size, &asset.AssetType,
		&asset.ContentType, &asset.DisplayName, &asset.Status, &asset.file, &asset.compressed)
	if err != nil {
		return apiAsset{}, err
	}
	asset.DateCreated = date.UTC().Format("2006-01-02T15:04:05.000000Z")
	if size.Valid {
		asset.FileSize = &size.Int64
	}
	return asset, nil
}

func ownedBookmarkExists(r *http.Request, db *sql.DB, engine string, ownerID, bookmarkID int64) (bool, error) {
	query := "SELECT 1 FROM bookmarks_bookmark WHERE id = " + assetMarker(engine, 1) + " AND owner_id = " + assetMarker(engine, 2)
	var value int
	err := db.QueryRowContext(r.Context(), query, bookmarkID, ownerID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func serveBookmarkAssets(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, bookmarkID int64, path string, tokenAuth bool) {
	if path == "upload" && r.Method != http.MethodPost {
		writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
		return
	}
	exists, err := ownedBookmarkExists(r, db, cfg.DBEngine, user.ID, bookmarkID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if !exists {
		writeDetail(w, http.StatusNotFound, "Bookmark does not exist")
		return
	}
	if path == "upload" {
		serveAssetUpload(w, r, cfg, db, user, bookmarkID, tokenAuth)
		return
	}
	if path == "" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
			return
		}
		serveAssetList(w, r, cfg, db, bookmarkID)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) > 2 || len(parts) == 2 && parts[1] != "download" {
		http.NotFound(w, r)
		return
	}
	assetID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || assetID < 1 {
		http.NotFound(w, r)
		return
	}
	query := `SELECT id, bookmark_id, date_created, file_size, asset_type, content_type,
		display_name, status, file, gzip FROM bookmarks_bookmarkasset WHERE id = ` + assetMarker(cfg.DBEngine, 1) +
		` AND bookmark_id = ` + assetMarker(cfg.DBEngine, 2)
	asset, err := scanAsset(db.QueryRowContext(r.Context(), query, assetID, bookmarkID))
	if errors.Is(err, sql.ErrNoRows) {
		writeDetail(w, http.StatusNotFound, "No BookmarkAsset matches the given query.")
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
			return
		}
		serveAssetDownload(w, r, cfg, asset)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_ = writeJSON(w, asset)
		}
	case http.MethodDelete:
		if !tokenAuth && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		if err := deleteAsset(r, cfg, db, bookmarkID, asset); err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		w.Header().Del("Content-Type")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
	}
}

func serveAssetList(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, bookmarkID int64) {
	limit := positiveIntOr(r.URL.Query().Get("limit"), 100)
	offset := nonnegativeIntOr(r.URL.Query().Get("offset"), 0)
	marker := func(n int) string { return assetMarker(cfg.DBEngine, n) }
	var count int64
	if err := db.QueryRowContext(r.Context(), "SELECT count(*) FROM bookmarks_bookmarkasset WHERE bookmark_id = "+marker(1), bookmarkID).Scan(&count); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	query := `SELECT id, bookmark_id, date_created, file_size, asset_type, content_type,
		display_name, status, file, gzip FROM bookmarks_bookmarkasset WHERE bookmark_id = ` + marker(1) +
		` ORDER BY id LIMIT ` + marker(2) + ` OFFSET ` + marker(3)
	rows, err := db.QueryContext(r.Context(), query, bookmarkID, limit, offset)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	defer rows.Close()
	results := make([]apiAsset, 0)
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		results = append(results, asset)
	}
	if err := rows.Err(); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	var next, previous *string
	if int64(offset)+int64(limit) < count {
		value := pageURL(r, limit, offset+limit)
		next = &value
	}
	if offset > 0 {
		value := pageURL(r, limit, max(0, offset-limit))
		previous = &value
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_ = writeJSON(w, struct {
			Count    int64      `json:"count"`
			Next     *string    `json:"next"`
			Previous *string    `json:"previous"`
			Results  []apiAsset `json:"results"`
		}{count, next, previous, results})
	}
}

func serveAssetDownload(w http.ResponseWriter, r *http.Request, cfg config.Config, asset apiAsset) {
	serveAssetFile(w, r, cfg, asset, false)
}

func serveAssetFile(w http.ResponseWriter, r *http.Request, cfg config.Config, asset apiAsset, inline bool) {
	if asset.file == "" {
		writeDetail(w, http.StatusNotFound, "Asset file does not exist")
		return
	}
	root, err := os.OpenRoot(filepath.Join(cfg.DataDir, "assets"))
	if err != nil {
		writeDetail(w, http.StatusNotFound, "Asset file does not exist")
		return
	}
	defer root.Close()
	file, err := root.Open(asset.file)
	if err != nil {
		writeDetail(w, http.StatusNotFound, "Asset file does not exist")
		return
	}
	defer file.Close()
	var source io.Reader = file
	if asset.compressed {
		reader, err := gzip.NewReader(file)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		defer reader.Close()
		source = reader
	}
	name := asset.DisplayName
	if asset.AssetType == "snapshot" {
		extension := ".html"
		if asset.ContentType == "application/pdf" {
			extension = ".pdf"
		}
		name += extension
	}
	w.Header().Set("Content-Type", asset.ContentType)
	disposition := "attachment"
	if inline {
		disposition = "inline"
		switch {
		case strings.HasPrefix(asset.ContentType, "video/"):
			w.Header().Set("Content-Security-Policy", "default-src 'none'; media-src 'self';")
		case asset.ContentType == "application/pdf":
			w.Header().Set("Content-Security-Policy", "default-src 'none'; object-src 'self';")
		default:
			w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
		}
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, source)
	}
}

func deleteAsset(r *http.Request, cfg config.Config, db *sql.DB, bookmarkID int64, asset apiAsset) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	marker := func(n int) string { return assetMarker(cfg.DBEngine, n) }
	query := `UPDATE bookmarks_bookmark SET latest_snapshot_id = (
		SELECT id FROM bookmarks_bookmarkasset WHERE bookmark_id = ` + marker(1) +
		` AND asset_type = 'snapshot' AND status = 'complete' AND id <> ` + marker(2) +
		` ORDER BY date_created DESC, id DESC LIMIT 1), date_modified = ` + marker(3) +
		` WHERE id = ` + marker(4) + ` AND latest_snapshot_id = ` + marker(5)
	if _, err := tx.ExecContext(r.Context(), query, bookmarkID, asset.ID, time.Now().UTC(), bookmarkID, asset.ID); err != nil {
		return err
	}
	query = "DELETE FROM bookmarks_bookmarkasset WHERE id = " + marker(1) + " AND bookmark_id = " + marker(2)
	if _, err := tx.ExecContext(r.Context(), query, asset.ID, bookmarkID); err != nil {
		return err
	}
	query = "UPDATE bookmarks_bookmark SET date_modified = " + marker(1) + " WHERE id = " + marker(2)
	if _, err := tx.ExecContext(r.Context(), query, time.Now().UTC(), bookmarkID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	removeStoredFile(filepath.Join(cfg.DataDir, "assets"), asset.file)
	return nil
}

func serveAssetUpload(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, bookmarkID int64, tokenAuth bool) {
	if r.Method != http.MethodPost {
		writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
		return
	}
	if cfg.DisableAssetUpload {
		w.WriteHeader(http.StatusForbidden)
		_ = writeJSON(w, map[string]string{"error": "Asset upload is disabled."})
		return
	}
	if !tokenAuth && !verifyAPICSRF(r, cfg) {
		writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = writeJSON(w, map[string]string{"error": "No file provided."})
		return
	}
	defer file.Close()
	asset, err := storeUploadedAsset(r.Context(), cfg, db, bookmarkID, file, header)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = writeJSON(w, map[string]string{"error": "Failed to upload asset."})
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = writeJSON(w, asset)
	_ = user
}

func storeUploadedAsset(ctx context.Context, cfg config.Config, db *sql.DB, bookmarkID int64, source io.Reader, header *multipart.FileHeader) (apiAsset, error) {
	now := time.Now().UTC()
	name, extension := splitUploadedName(header.Filename)
	contentType := header.Header.Get("Content-Type")
	compressed := contentType != "application/gzip"
	if compressed {
		extension += ".gz"
	}
	filename := generatedAssetName("upload", now, name, extension)
	directory := filepath.Join(cfg.DataDir, "assets")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return apiAsset{}, err
	}
	temp, err := os.CreateTemp(directory, ".asset-upload-")
	if err != nil {
		return apiAsset{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if compressed {
		writer, err := gzip.NewWriterLevel(temp, gzip.BestCompression)
		if err != nil {
			temp.Close()
			return apiAsset{}, err
		}
		_, err = io.Copy(writer, source)
		closeErr := writer.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			temp.Close()
			return apiAsset{}, err
		}
	} else if _, err := io.Copy(temp, source); err != nil {
		temp.Close()
		return apiAsset{}, err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return apiAsset{}, err
	}
	info, err := temp.Stat()
	if err != nil {
		temp.Close()
		return apiAsset{}, err
	}
	if err := temp.Close(); err != nil {
		return apiAsset{}, err
	}
	finalPath := filepath.Join(directory, filename)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return apiAsset{}, err
	}
	keep := false
	defer func() {
		if !keep {
			os.Remove(finalPath)
		}
	}()
	marker := func(n int) string { return assetMarker(cfg.DBEngine, n) }
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apiAsset{}, err
	}
	defer tx.Rollback()
	query := `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (` + strings.Join([]string{marker(1), marker(2), marker(3), marker(4), marker(5), marker(6), marker(7), marker(8), marker(9)}, ",") + `) RETURNING id`
	var id int64
	if err := tx.QueryRowContext(ctx, query, now, filename, info.Size(), "upload", contentType, header.Filename, "complete", compressed, bookmarkID).Scan(&id); err != nil {
		return apiAsset{}, err
	}
	query = "UPDATE bookmarks_bookmark SET date_modified = " + marker(1) + " WHERE id = " + marker(2)
	if _, err := tx.ExecContext(ctx, query, time.Now().UTC(), bookmarkID); err != nil {
		return apiAsset{}, err
	}
	if err := tx.Commit(); err != nil {
		return apiAsset{}, err
	}
	keep = true
	size := info.Size()
	return apiAsset{ID: id, Bookmark: bookmarkID, DateCreated: now.Format("2006-01-02T15:04:05.000000Z"), FileSize: &size,
		AssetType: "upload", ContentType: contentType, DisplayName: header.Filename, Status: "complete", file: filename, compressed: compressed}, nil
}

func splitUploadedName(filename string) (string, string) {
	extension := filepath.Ext(filename)
	return strings.TrimSuffix(filename, extension), strings.TrimPrefix(extension, ".")
}

func generatedAssetName(assetType string, created time.Time, name, extension string) string {
	prefix := assetType + "_" + created.Format("2006-01-02_150405") + "_"
	var cleaned strings.Builder
	for _, char := range name {
		if char < 128 && (char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			cleaned.WriteRune(char)
		} else {
			cleaned.WriteByte('_')
		}
	}
	maxLength := 192 - len(prefix) - 1 - len(extension)
	if maxLength < 0 {
		maxLength = 0
	}
	value := cleaned.String()
	if len(value) > maxLength {
		value = value[:maxLength]
	}
	return prefix + value + "." + extension
}
