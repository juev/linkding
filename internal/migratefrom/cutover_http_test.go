package migratefrom

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/httpserver"
)

var csrfField = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([^"]+)"`)

func verifyMigratedHTTP(t *testing.T, db *sql.DB, engine, dataDir, username, password string) {
	t.Helper()
	cfg := config.Config{DBEngine: engine, DataDir: dataDir}
	server := httptest.NewServer(httpserver.New(db, cfg, t.TempDir()))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	login, err := client.Get(server.URL + "/login/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(login.Body)
	login.Body.Close()
	if err != nil || login.StatusCode != http.StatusOK {
		t.Fatalf("login page: status=%d err=%v", login.StatusCode, err)
	}
	match := csrfField.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("login page has no CSRF field")
	}
	form := url.Values{"username": {username}, "password": {password}, "csrfmiddlewaretoken": {string(match[1])}}
	afterLogin, err := client.PostForm(server.URL+"/login/", form)
	if err != nil {
		t.Fatal(err)
	}
	afterLogin.Body.Close()
	if afterLogin.StatusCode != http.StatusOK || afterLogin.Request.URL.Path != "/bookmarks" {
		t.Fatalf("migrated password login: status=%d path=%s", afterLogin.StatusCode, afterLogin.Request.URL.Path)
	}
	profile, err := client.Get(server.URL + "/api/user/profile/")
	if err != nil {
		t.Fatal(err)
	}
	profile.Body.Close()
	if profile.StatusCode != http.StatusOK {
		t.Fatalf("migrated session profile: %d", profile.StatusCode)
	}
	var apiToken, feedToken string
	var assetID int64
	for _, item := range []struct {
		query string
		value any
	}{
		{`SELECT key FROM bookmarks_apitoken ORDER BY id LIMIT 1`, &apiToken},
		{`SELECT key FROM bookmarks_feedtoken ORDER BY key LIMIT 1`, &feedToken},
		{`SELECT id FROM bookmarks_bookmarkasset WHERE status='complete' ORDER BY id LIMIT 1`, &assetID},
	} {
		if err := db.QueryRow(item.query).Scan(item.value); err != nil {
			t.Fatalf("migrated fixture lacks token or completed asset: %v", err)
		}
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/user/profile/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Token "+apiToken)
	withToken, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	withToken.Body.Close()
	if withToken.StatusCode != http.StatusOK {
		t.Fatalf("migrated API token: %d", withToken.StatusCode)
	}
	feed, err := client.Get(server.URL + "/feeds/" + url.PathEscape(feedToken) + "/all")
	if err != nil {
		t.Fatal(err)
	}
	feedBody, err := io.ReadAll(feed.Body)
	feed.Body.Close()
	if err != nil || feed.StatusCode != http.StatusOK || !strings.Contains(string(feedBody), "<rss") {
		t.Fatalf("migrated feed token: status=%d err=%v", feed.StatusCode, err)
	}
	asset, err := client.Get(server.URL + "/assets/" + strconv.FormatInt(assetID, 10))
	if err != nil {
		t.Fatal(err)
	}
	assetBody, err := io.ReadAll(asset.Body)
	asset.Body.Close()
	if err != nil || asset.StatusCode != http.StatusOK || len(assetBody) == 0 {
		t.Fatalf("migrated asset: status=%d bytes=%d err=%v", asset.StatusCode, len(assetBody), err)
	}
}
