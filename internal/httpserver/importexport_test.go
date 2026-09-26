package httpserver

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestSettingsImportExportSessionOwnerAndCSRF(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
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
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmark
		(url, url_normalized, title, description, notes, website_title, website_description, unread, is_archived,
		 shared, date_added, date_modified, owner_id, web_archive_snapshot_url, favicon_file, preview_image_file)
		 VALUES ('https://other.example', 'https://other.example', 'Other', '', '', NULL, NULL, 0, 0, 0, ?, ?, ?, '', '', '')`,
		time.Unix(1, 0).UTC(), time.Unix(1, 0).UTC(), bob.ID); err != nil {
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
	emptyImport := httptest.NewRequest(http.MethodGet, "/settings/import", nil)
	emptyImport.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, emptyImport)
	if emptyResponse.Code != http.StatusFound || emptyResponse.Header().Get("Location") != "/settings/general" {
		t.Fatalf("GET import without file: status %d redirect %q", emptyResponse.Code, emptyResponse.Header().Get("Location"))
	}
	generalRequest := httptest.NewRequest(http.MethodGet, "/settings/general", nil)
	generalRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	for _, cookie := range emptyResponse.Result().Cookies() {
		generalRequest.AddCookie(cookie)
	}
	generalResponse := httptest.NewRecorder()
	handler.ServeHTTP(generalResponse, generalRequest)
	if generalResponse.Code != http.StatusOK || !strings.Contains(generalResponse.Body.String(), "Please select a file to import.") {
		t.Fatalf("GET import must show the missing-file error on settings: %d", generalResponse.Code)
	}
	makeImport := func(withCSRF bool) *http.Request {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, err := writer.CreateFormFile("import_file", "bookmarks.html")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(file, `<DL><DT><A HREF="https://alice.example" ADD_DATE="1" PRIVATE="0" TAGS="Go">Alice</A></DL>`)
		_ = writer.WriteField("map_private_flag", "on")
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/settings/import", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		if withCSRF {
			request.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
			request.Header.Set("X-CSRFToken", csrf)
		}
		return request
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, makeImport(false))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("import without CSRF: %d", denied.Code)
	}
	imported := httptest.NewRecorder()
	handler.ServeHTTP(imported, makeImport(true))
	if imported.Code != http.StatusFound || imported.Header().Get("Location") != "/settings/general" {
		t.Fatalf("import: status %d redirect %q", imported.Code, imported.Header().Get("Location"))
	}
	var shared bool
	if err := db.QueryRowContext(ctx, `SELECT shared FROM bookmarks_bookmark WHERE owner_id = ? AND url = 'https://alice.example'`, alice.ID).Scan(&shared); err != nil || !shared {
		t.Fatalf("imported bookmark: shared=%t err=%v", shared, err)
	}
	exportReq := httptest.NewRequest(http.MethodGet, "/settings/export", nil)
	exportReq.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	exported := httptest.NewRecorder()
	handler.ServeHTTP(exported, exportReq)
	if exported.Code != http.StatusOK || !strings.Contains(exported.Body.String(), `HREF="https://alice.example"`) ||
		strings.Contains(exported.Body.String(), "other.example") || exported.Header().Get("Content-Type") != "text/plain; charset=UTF-8" ||
		!strings.HasPrefix(exported.Header().Get("Content-Disposition"), `attachment; filename="bookmarks_`) {
		t.Fatalf("export: status %d, headers %v, body %q", exported.Code, exported.Header(), exported.Body.String())
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/settings/export", nil))
	if unauthorized.Code != http.StatusFound || unauthorized.Header().Get("Location") != "/login?next=/settings/export" {
		t.Fatalf("unauthorized export: status %d redirect %q", unauthorized.Code, unauthorized.Header().Get("Location"))
	}
}
