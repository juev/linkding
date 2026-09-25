package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/juev/linkding/internal/jobs"
)

var waybackPath = regexp.MustCompile(`^/web/[0-9]{14}/.+`)
var waybackLink = regexp.MustCompile(`web\.archive\.org/web/[0-9]{14}/[^> ]+`)

type waybackPayload struct {
	BookmarkID  int64 `json:"bookmark_id"`
	ForceUpdate bool  `json:"force_update"`
}

func (p *Processor) SaveWebArchive(ctx context.Context, job jobs.Job) error {
	var payload waybackPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.BookmarkID < 1 {
		return fmt.Errorf("invalid web archive job payload")
	}
	id, pageURL, existing, err := p.bookmarkForJob(ctx, job, "web_archive_snapshot_url")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != "" && !payload.ForceUpdate {
		return nil
	}
	base := p.waybackBase
	if base == "" {
		base = "https://web.archive.org"
	}
	target := strings.TrimRight(base, "/") + "/save/" + strings.ReplaceAll(strings.TrimSpace(pageURL), " ", "%20")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.0.0 Safari/537.36")
	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == 509 {
		return nil
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("Wayback response status %d", response.StatusCode)
	}
	archiveURL := ""
	if path := response.Header.Get("Content-Location"); waybackPath.MatchString(path) {
		archiveURL = "https://web.archive.org" + path
	} else if match := waybackLink.FindString(response.Header.Get("Link")); match != "" {
		archiveURL = "https://" + match
	} else if response.Request != nil && response.Request.URL != nil {
		path := response.Request.URL.Path
		if waybackPath.MatchString(path) {
			archiveURL = "https://web.archive.org" + path
		}
	}
	if archiveURL == "" {
		return fmt.Errorf("Wayback response has no archive URL")
	}
	return p.updateFile(ctx, id, pageURL, "web_archive_snapshot_url", archiveURL)
}
