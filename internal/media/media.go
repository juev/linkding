package media

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpclient"
	"github.com/juev/linkding/internal/jobs"
	"github.com/juev/linkding/internal/metadata"
)

type Processor struct {
	db          *sql.DB
	engine      string
	config      config.Config
	waybackBase string
}

func New(db *sql.DB, engine string, cfg config.Config) *Processor {
	return &Processor{db: db, engine: engine, config: cfg, waybackBase: "https://web.archive.org"}
}

func (p *Processor) Handlers() map[string]jobs.Handler {
	return map[string]jobs.Handler{
		"web_archive_snapshot": p.SaveWebArchive,
		"load_favicon":         p.LoadFavicon,
		"load_preview_image":   p.LoadPreviewImage,
		"refresh_metadata":     p.RefreshMetadata,
		"process_snapshot":     p.ProcessSnapshot,
	}
}

func (p *Processor) RefreshMetadata(ctx context.Context, job jobs.Job) error {
	var payload bookmarkPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	var pageURL string
	query := `SELECT url FROM bookmarks_bookmark WHERE id = ` + p.marker(1)
	err := p.db.QueryRowContext(ctx, query, payload.BookmarkID).Scan(&pageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	client := httpclient.New(p.config.AllowedInternalHosts, 10*time.Second)
	meta := metadata.Load(ctx, client, pageURL)
	query = `UPDATE bookmarks_bookmark SET title = CASE WHEN ` + p.marker(1) + ` <> '' THEN ` + p.marker(2) + ` ELSE title END,
		description = CASE WHEN ` + p.marker(3) + ` <> '' THEN ` + p.marker(4) + ` ELSE description END,
		date_modified = ` + p.marker(5) + ` WHERE id = ` + p.marker(6)
	_, err = p.db.ExecContext(ctx, query, meta.Title, meta.Title, meta.Description, meta.Description, time.Now().UTC(), payload.BookmarkID)
	return err
}

func (p *Processor) marker(n int) string {
	if p.engine == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

type bookmarkPayload struct {
	BookmarkID int64 `json:"bookmark_id"`
}

func (p *Processor) bookmarkForJob(ctx context.Context, job jobs.Job, field string) (int64, string, string, error) {
	var payload bookmarkPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.BookmarkID < 1 {
		return 0, "", "", fmt.Errorf("invalid bookmark job payload")
	}
	query := "SELECT owner_id, url, " + field + " FROM bookmarks_bookmark WHERE id = " + p.marker(1)
	var ownerID int64
	var pageURL, currentFile string
	err := p.db.QueryRowContext(ctx, query, payload.BookmarkID).Scan(&ownerID, &pageURL, &currentFile)
	return payload.BookmarkID, pageURL, currentFile, err
}

func (p *Processor) updateFile(ctx context.Context, id int64, pageURL, column, filename string) error {
	query := "UPDATE bookmarks_bookmark SET " + column + " = " + p.marker(1) + " WHERE id = " + p.marker(2) + " AND url = " + p.marker(3)
	_, err := p.db.ExecContext(ctx, query, filename, id, pageURL)
	return err
}

func (p *Processor) LoadFavicon(ctx context.Context, job jobs.Job) error {
	id, pageURL, currentFile, err := p.bookmarkForJob(ctx, job, "favicon_file")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	filename, err := p.loadFavicon(ctx, pageURL)
	if err != nil {
		return err
	}
	if filename == currentFile {
		return nil
	}
	return p.updateFile(ctx, id, pageURL, "favicon_file", filename)
}

func (p *Processor) loadFavicon(ctx context.Context, pageURL string) (string, error) {
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid bookmark URL for favicon")
	}
	baseURL := parsed.Scheme + "://" + parsed.Hostname()
	baseName := pythonWordFilename(baseURL)
	directory := filepath.Join(p.config.DataDir, "favicons")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())) != baseName {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if time.Since(info.ModTime()) < 24*time.Hour {
			return entry.Name(), nil
		}
	}
	providerURL := strings.ReplaceAll(p.config.FaviconProvider, "{url}", baseURL)
	providerURL = strings.ReplaceAll(providerURL, "{domain}", parsed.Hostname())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, providerURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	contentType := strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0])
	extension, ok := imageExtension(contentType)
	if !ok {
		return "", fmt.Errorf("unsupported favicon content type %q", contentType)
	}
	filename := baseName + extension
	if err := writeAtomic(directory, filename, response.Body, 10<<20); err != nil {
		return "", err
	}
	return filename, nil
}

func pythonWordFilename(value string) string {
	var result strings.Builder
	separator := false
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' {
			if separator && result.Len() > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(char)
			separator = false
		} else {
			separator = true
		}
	}
	if separator {
		result.WriteByte('_')
	}
	return result.String()
}

func imageExtension(contentType string) (string, bool) {
	switch strings.ToLower(contentType) {
	case "image/png":
		return ".png", true
	case "image/jpeg", "image/jpg":
		return ".jpg", true
	case "image/gif":
		return ".gif", true
	case "image/svg+xml":
		return ".svg", true
	case "image/webp":
		return ".webp", true
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico", true
	default:
		return "", false
	}
}

func (p *Processor) LoadPreviewImage(ctx context.Context, job jobs.Job) error {
	id, pageURL, currentFile, err := p.bookmarkForJob(ctx, job, "preview_image_file")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	filename, err := p.loadPreview(ctx, pageURL)
	if err != nil {
		return err
	}
	if filename == currentFile {
		return nil
	}
	return p.updateFile(ctx, id, pageURL, "preview_image_file", filename)
}

func (p *Processor) loadPreview(ctx context.Context, pageURL string) (string, error) {
	guarded := httpclient.New(p.config.AllowedInternalHosts, 10*time.Second)
	metadata := metadata.Load(ctx, guarded, pageURL)
	if metadata.PreviewImage == "" {
		return "", nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadata.PreviewImage, nil)
	if err != nil {
		return "", err
	}
	response, err := guarded.Do(request)
	if err != nil {
		var blocked *httpclient.BlockedAddressError
		if errors.As(err, &blocked) {
			return "", nil
		}
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil
	}
	size := response.ContentLength
	if size < 0 || size > p.config.PreviewMaxSize {
		return "", nil
	}
	extension, ok := imageExtension(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if !ok || (extension != ".jpg" && extension != ".png" && extension != ".gif" && extension != ".svg" && extension != ".webp") {
		return "", nil
	}
	hash := md5.Sum([]byte(pageURL))
	filename := hex.EncodeToString(hash[:]) + extension
	directory := filepath.Join(p.config.DataDir, "previews")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", err
	}
	if err := writeAtomic(directory, filename, response.Body, size); err != nil {
		return "", err
	}
	return filename, nil
}

func writeAtomic(directory, filename string, source io.Reader, maxBytes int64) error {
	temp, err := os.CreateTemp(directory, ".linkding-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if maxBytes < 0 {
		temp.Close()
		return fmt.Errorf("negative file limit")
	}
	written, err := io.Copy(temp, io.LimitReader(source, maxBytes+1))
	if err != nil {
		temp.Close()
		return err
	}
	if written > maxBytes {
		temp.Close()
		return fmt.Errorf("file exceeds size limit")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(directory, filename))
}
