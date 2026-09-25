package httpserver

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpclient"
	"github.com/juev/linkding/internal/metadata"
)

func serveSingleFileUpload(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, user auth.User, repo *bookmarks.Repository, tokenAuth bool) {
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
	pageURL := r.FormValue("url")
	file, _, err := r.FormFile("file")
	if pageURL == "" || err != nil {
		if file != nil {
			file.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = writeJSON(w, map[string]string{"error": "Both 'url' and 'file' parameters are required."})
		return
	}
	defer file.Close()
	bookmark, err := repo.FindExisting(r.Context(), user.ID, pageURL)
	if errors.Is(err, sql.ErrNoRows) {
		bookmark, _, err = repo.CreateOrUpdateData(r.Context(), user.ID, bookmarks.CreateInput{URL: pageURL, DisableHTMLSnapshot: true})
		if err == nil {
			client := httpclient.New(cfg.AllowedInternalHosts, 10*time.Second)
			meta := metadata.Load(r.Context(), client, bookmark.URL)
			bookmark, err = repo.EnhanceMetadata(r.Context(), user.ID, bookmark.ID, meta.Title, meta.Description)
		}
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if err := storeSnapshotUpload(r.Context(), cfg, db, bookmark.ID, bookmark.URL, file); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = writeJSON(w, map[string]string{"message": "Snapshot uploaded successfully."})
}

func storeSnapshotUpload(ctx context.Context, cfg config.Config, db *sql.DB, bookmarkID int64, pageURL string, source io.Reader) error {
	now := time.Now().UTC()
	filename := generatedAssetName("snapshot", now, pageURL, "html.gz")
	directory := filepath.Join(cfg.DataDir, "assets")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".singlefile-upload-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	writer, err := gzip.NewWriterLevel(temp, gzip.BestCompression)
	if err != nil {
		temp.Close()
		return err
	}
	_, copyErr := io.Copy(writer, source)
	closeWriterErr := writer.Close()
	if copyErr != nil {
		temp.Close()
		return copyErr
	}
	if closeWriterErr != nil {
		temp.Close()
		return closeWriterErr
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	info, err := temp.Stat()
	if err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	finalPath := filepath.Join(directory, filename)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return err
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
		return err
	}
	defer tx.Rollback()
	query := `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (` + strings.Join([]string{marker(1), marker(2), marker(3), marker(4), marker(5), marker(6), marker(7), marker(8), marker(9)}, ",") + `) RETURNING id`
	var assetID int64
	if err := tx.QueryRowContext(ctx, query, now, filename, info.Size(), "snapshot", "text/html",
		"HTML snapshot from "+now.Format("01/02/2006"), "complete", true, bookmarkID).Scan(&assetID); err != nil {
		return err
	}
	query = "UPDATE bookmarks_bookmark SET latest_snapshot_id = " + marker(1) + ", date_modified = " + marker(2) + " WHERE id = " + marker(3)
	if _, err := tx.ExecContext(ctx, query, assetID, time.Now().UTC(), bookmarkID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	keep = true
	return nil
}
