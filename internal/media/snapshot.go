package media

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juev/linkding/internal/httpclient"
	"github.com/juev/linkding/internal/jobs"
)

type snapshotPayload struct {
	AssetID int64 `json:"asset_id"`
}

type snapshotAsset struct {
	ID, BookmarkID int64
	Created        time.Time
	Status, URL    string
}

func (p *Processor) loadSnapshotAsset(ctx context.Context, id int64) (snapshotAsset, error) {
	query := `SELECT a.id, a.bookmark_id, a.date_created, a.status, b.url
		FROM bookmarks_bookmarkasset a JOIN bookmarks_bookmark b ON b.id = a.bookmark_id
		WHERE a.id = ` + p.marker(1) + ` AND a.asset_type = 'snapshot'`
	var asset snapshotAsset
	err := p.db.QueryRowContext(ctx, query, id).Scan(&asset.ID, &asset.BookmarkID, &asset.Created, &asset.Status, &asset.URL)
	return asset, err
}

func (p *Processor) markSnapshotFailure(ctx context.Context, id int64) error {
	query := "UPDATE bookmarks_bookmarkasset SET status = 'failure' WHERE id = " + p.marker(1) + " AND status = 'pending'"
	_, err := p.db.ExecContext(ctx, query, id)
	return err
}

func (p *Processor) ProcessSnapshot(ctx context.Context, job jobs.Job) (resultErr error) {
	// Upstream leaves already queued assets pending while snapshots are disabled.
	if !p.config.EnableSnapshots {
		return nil
	}
	var payload snapshotPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.AssetID < 1 {
		return fmt.Errorf("invalid snapshot job payload")
	}
	asset, err := p.loadSnapshotAsset(ctx, payload.AssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if asset.Status != "pending" {
		return nil
	}
	release, err := p.acquireSnapshotLock(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	asset, err = p.loadSnapshotAsset(ctx, payload.AssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if asset.Status != "pending" {
		return nil
	}
	defer func() {
		if resultErr != nil && ctx.Err() == nil {
			failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, p.markSnapshotFailure(failureCtx, asset.ID))
		}
	}()
	directory := filepath.Join(p.config.DataDir, "assets")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	contentType, err := p.detectContentType(ctx, asset.URL)
	if err != nil {
		return err
	}
	pdf := contentType == "application/pdf" || contentType == "application/x-pdf"
	temp, err := os.CreateTemp(directory, ".snapshot-source-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}
	defer os.Remove(tempPath)
	if pdf {
		if err := p.downloadPDF(ctx, asset.URL, tempPath); err != nil {
			return err
		}
	} else {
		if err := os.Remove(tempPath); err != nil {
			return err
		}
		if err := p.captureHTML(ctx, asset.URL, tempPath); err != nil {
			return err
		}
	}
	extension, resultType, label := "html.gz", "text/html", "HTML snapshot"
	if pdf {
		extension, resultType, label = "pdf.gz", "application/pdf", "PDF download"
	}
	filename := snapshotFilename(asset.Created, asset.URL, extension)
	finalPath := filepath.Join(directory, filename)
	if err := gzipSnapshot(tempPath, finalPath); err != nil {
		return err
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	update := `UPDATE bookmarks_bookmarkasset SET status = ` + p.marker(1) + `, content_type = ` + p.marker(2) + `,
		display_name = ` + p.marker(3) + `, file = ` + p.marker(4) + `, file_size = ` + p.marker(5) + `, gzip = ` + p.marker(6) +
		` WHERE id = ` + p.marker(7) + ` AND status = 'pending'`
	name := label + " from " + asset.Created.Format("01/02/2006")
	write, err := tx.ExecContext(ctx, update, "complete", resultType, name, filename, info.Size(), true, asset.ID)
	if err != nil {
		return err
	}
	changed, err := write.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("snapshot asset %d no longer pending", asset.ID)
	}
	update = "UPDATE bookmarks_bookmark SET latest_snapshot_id = " + p.marker(1) + ", date_modified = " + p.marker(2) + " WHERE id = " + p.marker(3)
	if _, err := tx.ExecContext(ctx, update, asset.ID, time.Now().UTC(), asset.BookmarkID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (p *Processor) detectContentType(ctx context.Context, pageURL string) (string, error) {
	client := httpclient.New(p.config.AllowedInternalHosts, 10*time.Second)
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		request, err := http.NewRequestWithContext(ctx, method, pageURL, nil)
		if err != nil {
			return "", err
		}
		request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.0.0 Safari/537.36")
		response, err := client.Do(request)
		if err != nil {
			var blocked *httpclient.BlockedAddressError
			if errors.As(err, &blocked) {
				return "", err
			}
			continue
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0])), nil
		}
	}
	return "", nil
}

func (p *Processor) downloadPDF(ctx context.Context, pageURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return err
	}
	client := httpclient.New(p.config.AllowedInternalHosts, 60*time.Second)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("PDF download status %d", response.StatusCode)
	}
	maxSize := p.config.SnapshotPDFMaxSize
	if maxSize <= 0 {
		maxSize = 15728640
	}
	if response.ContentLength > maxSize {
		return fmt.Errorf("PDF exceeds %d bytes", maxSize)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, maxSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxSize {
		return fmt.Errorf("PDF exceeds %d bytes", maxSize)
	}
	return nil
}

func (p *Processor) captureHTML(ctx context.Context, pageURL, destination string) error {
	options, err := splitShellWords(p.config.SingleFileUblockOptions)
	if err != nil {
		return err
	}
	extra, err := splitShellWords(p.config.SingleFileOptions)
	if err != nil {
		return err
	}
	args := append(append(options, extra...), pageURL, destination)
	path := p.config.SingleFilePath
	if path == "" {
		path = "single-file"
	}
	timeout := p.config.SingleFileTimeoutSec
	if timeout <= 0 {
		timeout = 120
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
	defer cancel()
	if err := runSingleFileProcess(runCtx, path, args); err != nil {
		return err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return fmt.Errorf("single-file did not create snapshot: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("single-file output is not a regular file")
	}
	return nil
}

func snapshotFilename(created time.Time, pageURL, extension string) string {
	prefix := "snapshot_" + created.Format("2006-01-02_150405") + "_"
	var value strings.Builder
	for _, char := range pageURL {
		if char < 128 && (char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			value.WriteRune(char)
		} else {
			value.WriteByte('_')
		}
	}
	name := value.String()
	maxLength := 192 - len(prefix) - 1 - len(extension)
	if len(name) > maxLength {
		name = name[:maxLength]
	}
	return prefix + name + "." + extension
}

func gzipSnapshot(sourcePath, destination string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".snapshot-gzip-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	compressor, err := gzip.NewWriterLevel(temp, gzip.BestCompression)
	if err != nil {
		temp.Close()
		return err
	}
	if _, err := io.Copy(compressor, source); err != nil {
		compressor.Close()
		temp.Close()
		return err
	}
	if err := compressor.Close(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, destination)
}
