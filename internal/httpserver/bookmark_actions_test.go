package httpserver

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkActionsOwnerBulkAndSearchPreference(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), EnableSnapshots: true}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	a, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://a.example", TagNames: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := repo.CreateOrUpdateData(ctx, bob.ID, bookmarks.CreateInput{URL: "https://b.example"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	post := func(path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		if withCSRF {
			form.Set("csrfmiddlewaretoken", csrf)
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		if withCSRF {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := post("/bookmarks/action", url.Values{"archive": {strconv.FormatInt(a.ID, 10)}}, false); got.Code != 403 {
		t.Fatalf("action without CSRF: %d", got.Code)
	}
	if got := post("/bookmarks/action", url.Values{"archive": {strconv.FormatInt(b.ID, 10)}}, true); got.Code != 404 {
		t.Fatalf("foreign bookmark action: %d", got.Code)
	}
	if got := post("/bookmarks/action", url.Values{"archive": {strconv.FormatInt(a.ID, 10)}}, true); got.Code != 302 || got.Header().Get("Location") != "/bookmarks" {
		t.Fatalf("archive action: %d %q", got.Code, got.Header().Get("Location"))
	}
	archived, err := repo.GetByID(ctx, alice.ID, a.ID)
	if err != nil || !archived.IsArchived {
		t.Fatalf("archive state: %+v %v", archived, err)
	}
	form := url.Values{"bulk_execute": {""}, "bulk_action": {"bulk_tag"}, "bulk_tag_string": {"new"}, "bookmark_id": {strconv.FormatInt(a.ID, 10), strconv.FormatInt(b.ID, 10)}}
	if got := post("/bookmarks/archived/action", form, true); got.Code != 302 {
		t.Fatalf("bulk tag: %d", got.Code)
	}
	updated, err := repo.GetByID(ctx, alice.ID, a.ID)
	if err != nil || !slices.Contains(updated.TagNames, "new") {
		t.Fatalf("bulk tag owner: %+v %v", updated, err)
	}
	foreign, err := repo.GetByID(ctx, bob.ID, b.ID)
	if err != nil || slices.Contains(foreign.TagNames, "new") {
		t.Fatalf("bulk tag foreign: %+v %v", foreign, err)
	}
	form = url.Values{"bulk_execute": {""}, "bulk_action": {"bulk_snapshot"}, "bookmark_id": {strconv.FormatInt(a.ID, 10), strconv.FormatInt(b.ID, 10)}}
	if got := post("/bookmarks/archived/action", form, true); got.Code != 302 {
		t.Fatalf("bulk snapshot: %d", got.Code)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmarkasset WHERE bookmark_id=?`, a.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("owner snapshot: %d %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmarkasset WHERE bookmark_id=?`, b.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign snapshot: %d %v", count, err)
	}
	searchForm := url.Values{"save": {""}, "sort": {"title_asc"}, "shared": {"off"}, "unread": {"yes"}, "q": {"example"}}
	if got := post("/bookmarks", searchForm, true); got.Code != 302 {
		t.Fatalf("save search preferences: %d", got.Code)
	}
	var prefs string
	if err := db.QueryRowContext(ctx, `SELECT search_preferences FROM bookmarks_userprofile WHERE user_id=?`, alice.ID).Scan(&prefs); err != nil || !strings.Contains(prefs, `"sort":"title_asc"`) {
		t.Fatalf("stored preferences: %q %v", prefs, err)
	}
	state := url.Values{"update_state": {strconv.FormatInt(a.ID, 10)}, "unread": {"on"}, "shared": {"on"}}
	if got := post("/bookmarks/archived/action?details="+strconv.FormatInt(a.ID, 10), state, true); got.Code != 302 {
		t.Fatalf("details state update: %d %q", got.Code, got.Body.String())
	}
	updated, err = repo.GetByID(ctx, alice.ID, a.ID)
	if err != nil || updated.IsArchived || !updated.Unread || !updated.Shared {
		t.Fatalf("details state: %+v %v", updated, err)
	}
	if got := post("/bookmarks/action", url.Values{"update_state": {strconv.FormatInt(b.ID, 10)}, "unread": {"on"}}, true); got.Code != 404 {
		t.Fatalf("foreign state update: %d", got.Code)
	}
	var upload bytes.Buffer
	multipartForm := multipart.NewWriter(&upload)
	for name, value := range map[string]string{"upload_asset": strconv.FormatInt(a.ID, 10), "update_state": strconv.FormatInt(a.ID, 10), "csrfmiddlewaretoken": csrf} {
		if err := multipartForm.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := multipartForm.CreateFormFile("upload_asset_file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello asset")); err != nil {
		t.Fatal(err)
	}
	if err := multipartForm.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/bookmarks/action?details="+strconv.FormatInt(a.ID, 10), &upload)
	request.Header.Set("Content-Type", multipartForm.FormDataContentType())
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 302 {
		t.Fatalf("details upload: %d %q", response.Code, response.Body.String())
	}
	var assetID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_bookmarkasset WHERE bookmark_id=? AND asset_type='upload'`, a.ID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if got := post("/bookmarks/action", url.Values{"remove_asset": {strconv.FormatInt(assetID, 10)}}, true); got.Code != 302 {
		t.Fatalf("details remove asset: %d", got.Code)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks_bookmarkasset WHERE id=?`, assetID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("removed asset count: %d %v", count, err)
	}
	streamForm := url.Values{"archive": {strconv.FormatInt(a.ID, 10)}, "csrfmiddlewaretoken": {csrf}}
	streamRequest := httptest.NewRequest(http.MethodPost, "/bookmarks/action", strings.NewReader(streamForm.Encode()))
	streamRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	streamRequest.Header.Set("Accept", "text/vnd.turbo-stream.html, text/html")
	streamRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	streamRequest.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	streamResponse := httptest.NewRecorder()
	handler.ServeHTTP(streamResponse, streamRequest)
	streamBody := streamResponse.Body.String()
	for _, target := range []string{`target="bookmark-list-container"`, `target="tag-cloud-container"`, `target="details-modal"`} {
		if !strings.Contains(streamBody, target) {
			t.Errorf("Turbo action missing %s", target)
		}
	}
	if streamResponse.Code != http.StatusOK || streamResponse.Header().Get("Content-Type") != "text/vnd.turbo-stream.html" ||
		streamResponse.Header().Get("Location") != "" || strings.Count(streamBody, "<turbo-stream ") != 3 ||
		strings.Contains(streamBody, "https://a.example") {
		t.Fatalf("Turbo action: %d Content-Type=%q Location=%q body=%q", streamResponse.Code, streamResponse.Header().Get("Content-Type"), streamResponse.Header().Get("Location"), streamBody)
	}
}
