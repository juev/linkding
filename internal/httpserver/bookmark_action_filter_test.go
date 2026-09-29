package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkActionsPreserveSearchAfterBulkArchive(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, cfg.DBEngine)
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET items_per_page=1 WHERE user_id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, cfg.DBEngine)
	var inboxIDs []int64
	for _, title := range []string{"Inbox One", "Inbox Two", "Inbox Three"} {
		item, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://" + strings.ReplaceAll(strings.ToLower(title), " ", "-") + ".example", Title: title, TagNames: []string{"inbox"}})
		if err != nil {
			t.Fatal(err)
		}
		inboxIDs = append(inboxIDs, item.ID)
	}
	other, _, err := repo.CreateOrUpdateData(ctx, user.ID, bookmarks.CreateInput{URL: "https://unrelated.example", Title: "Unrelated", TagNames: []string{"other"}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	get := httptest.NewRequest(http.MethodGet, "/bookmarks?q=%23inbox&page=1", nil)
	get.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, get)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `data-bookmarks-total="3"`) {
		t.Fatalf("initial filtered page: %d %q", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `action="/bookmarks/action?q=%23inbox&amp;page=1"`) {
		t.Fatal("bookmark action form must submit the current search and page")
	}
	for _, path := range []string{"/bookmarks/archived", "/bookmarks/shared"} {
		request := httptest.NewRequest(http.MethodGet, path+"?q=%23inbox&details=999", nil)
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `action="`+path+`/action?q=%23inbox"`) {
			t.Fatalf("%s action must preserve search without modal state: %d", path, response.Code)
		}
	}
	searchURL := "/bookmarks?q=%23inbox&user=alice&bundle=17&sort=title_asc&shared=yes&unread=yes&modified_since=2026-01-01&added_since=2026-01-02"
	searchRequest := httptest.NewRequest(http.MethodGet, searchURL, nil)
	searchRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	searchPage := httptest.NewRecorder()
	handler.ServeHTTP(searchPage, searchRequest)
	if searchPage.Code != http.StatusOK {
		t.Fatalf("search forms: %d", searchPage.Code)
	}
	searchStart := strings.Index(searchPage.Body.String(), `<form id="search"`)
	if searchStart < 0 {
		t.Fatal("missing search form")
	}
	searchForm, _, _ := strings.Cut(searchPage.Body.String()[searchStart:], `</form>`)
	preferencesStart := strings.Index(searchPage.Body.String(), `<form id="search_preferences"`)
	if preferencesStart < 0 {
		t.Fatal("missing search preferences form")
	}
	preferencesForm, _, _ := strings.Cut(searchPage.Body.String()[preferencesStart:], `</form>`)
	for name, value := range map[string]string{"user": "alice", "bundle": "17", "sort": "title_asc", "shared": "yes", "unread": "yes", "modified_since": "2026-01-01", "added_since": "2026-01-02"} {
		if !strings.Contains(searchForm, `type="hidden" name="`+name+`" value="`+value+`"`) {
			t.Errorf("search form drops %s", name)
		}
	}
	for name, value := range map[string]string{"q": "#inbox", "user": "alice", "bundle": "17", "modified_since": "2026-01-01", "added_since": "2026-01-02"} {
		if !strings.Contains(preferencesForm, `type="hidden" name="`+name+`" value="`+value+`"`) {
			t.Errorf("search preferences form drops %s", name)
		}
	}
	form := url.Values{"bulk_execute": {""}, "bulk_action": {"bulk_archive"}, "bookmark_id": {strconv.FormatInt(inboxIDs[2], 10)}, "csrfmiddlewaretoken": {csrf}}
	post := httptest.NewRequest(http.MethodPost, "/bookmarks/action?q=%23inbox&page=1", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Accept", "text/vnd.turbo-stream.html, text/html")
	post.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	post.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	stream := httptest.NewRecorder()
	handler.ServeHTTP(stream, post)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), `data-bookmarks-total="2"`) || strings.Contains(stream.Body.String(), `data-bookmark-id="`+strconv.FormatInt(other.ID, 10)+`"`) || !strings.Contains(stream.Body.String(), `q=%23inbox&amp;page=2`) {
		t.Fatalf("filtered Turbo update: %d %q", stream.Code, stream.Body.String())
	}
	post = httptest.NewRequest(http.MethodPost, "/bookmarks/action?q=%23inbox&page=1", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	post.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, post)
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "/bookmarks?q=%23inbox&page=1" {
		t.Fatalf("filtered non-Turbo redirect: %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	form.Del("bookmark_id")
	form.Set("bulk_select_across", "on")
	post = httptest.NewRequest(http.MethodPost, "/bookmarks/action?q=%23inbox&page=2", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	post.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	across := httptest.NewRecorder()
	handler.ServeHTTP(across, post)
	if across.Code != http.StatusFound || across.Header().Get("Location") != "/bookmarks?q=%23inbox&page=2" {
		t.Fatalf("filtered cross-page redirect: %d %q", across.Code, across.Header().Get("Location"))
	}
	for _, id := range inboxIDs {
		item, err := repo.GetByID(ctx, user.ID, id)
		if err != nil || !item.IsArchived {
			t.Fatalf("filtered cross-page archive for %d: %+v %v", id, item, err)
		}
	}
	item, err := repo.GetByID(ctx, user.ID, other.ID)
	if err != nil || item.IsArchived {
		t.Fatalf("cross-page archive affected unrelated bookmark: %+v %v", item, err)
	}
}
