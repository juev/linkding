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
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkFormCreateEditAndOwnership(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET default_mark_unread=1,enable_sharing=1 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherSession, err := users.CreateSession(ctx, bob.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path, session string, form url.Values) *http.Request {
		t.Helper()
		var body strings.Reader
		if form != nil {
			body = *strings.NewReader(form.Encode())
		} else {
			body = *strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, &body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		return r
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, request("GET", "/bookmarks/new?url=https%3A%2F%2Fexample.com&auto_close", session, nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `class="bookmarks-form-page"`) || !strings.Contains(get.Body.String(), `value="https://example.com"`) || !strings.Contains(get.Body.String(), `name="auto_close" value="True"`) || !strings.Contains(get.Body.String(), `name="unread" id="id_unread" checked`) {
		t.Fatalf("new form: %d %q", get.Code, get.Body.String())
	}
	notesStart := strings.Index(get.Body.String(), `<details class="notes"`)
	if notesStart < 0 {
		t.Fatal("new form has no collapsible notes")
	}
	notesHTML := get.Body.String()[notesStart:]
	notesEnd := strings.Index(notesHTML, "</details>")
	help := strings.Index(notesHTML, "Additional notes, supports Markdown.")
	if notesEnd < 0 || help < 0 || help > notesEnd || !strings.Contains(notesHTML[:notesEnd], `aria-describedby="id_notes_help"`) {
		t.Fatal("notes help must be inside the collapsed details")
	}
	invalid := url.Values{"url": {"bad-url"}, "tag_string": {"go test"}, "csrfmiddlewaretoken": {csrf}}
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, request("POST", "/bookmarks/new", session, invalid))
	if bad.Code != 422 || !strings.Contains(bad.Body.String(), `class="form-input is-error" autocomplete="off" aria-describedby="id_url_error" aria-invalid="true"`) || !strings.Contains(bad.Body.String(), `<ul class="errorlist form-input-hint is-error" id="id_url_error"><li>Enter a valid URL.</li></ul>`) {
		t.Fatalf("invalid form: %d %q", bad.Code, bad.Body.String())
	}
	form := url.Values{"url": {"https://example.com"}, "title": {"Example"}, "description": {"Description"}, "notes": {"notes"}, "tag_string": {"go test"}, "unread": {"on"}, "shared": {"on"}, "auto_close": {"True"}, "csrfmiddlewaretoken": {csrf}}
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request("POST", "/bookmarks/new", session, form))
	if created.Code != 302 || created.Header().Get("Location") != "/bookmarks/close" {
		t.Fatalf("created: %d %q", created.Code, created.Header().Get("Location"))
	}
	closePage := httptest.NewRecorder()
	handler.ServeHTTP(closePage, request("GET", "/bookmarks/close", session, nil))
	if closePage.Code != 200 || !strings.Contains(closePage.Body.String(), "You can now close this window.") ||
		!strings.Contains(closePage.Body.String(), ">Add bookmark</a>") ||
		!strings.Contains(closePage.Body.String(), `aria-label="Navigation menu"`) ||
		!strings.Contains(closePage.Body.String(), ">Shared</a>") {
		t.Fatalf("close page must retain the application navigation: %d", closePage.Code)
	}
	var id int64
	var title string
	var unread, shared bool
	if err := db.QueryRowContext(ctx, `SELECT id,title,unread,shared FROM bookmarks_bookmark WHERE owner_id=?`, alice.ID).Scan(&id, &title, &unread, &shared); err != nil || title != "Example" || !unread || !shared {
		t.Fatalf("stored bookmark: %d %q %t %t %v", id, title, unread, shared, err)
	}
	editPath := "/bookmarks/" + strconv.FormatInt(id, 10) + "/edit"
	edit := httptest.NewRecorder()
	handler.ServeHTTP(edit, request("GET", editPath+"?return_url=%2Fbookmarks%3Fq%3Dgo", session, nil))
	if edit.Code != 200 || !strings.Contains(edit.Body.String(), `value="Example"`) || !strings.Contains(edit.Body.String(), `href="/bookmarks?q=go"`) {
		t.Fatalf("edit form: %d %q", edit.Code, edit.Body.String())
	}
	other := httptest.NewRecorder()
	handler.ServeHTTP(other, request("GET", editPath, otherSession, nil))
	if other.Code != 404 {
		t.Fatalf("other user edit: %d", other.Code)
	}
	form.Set("title", "Edited")
	form.Set("auto_close", "False")
	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, request("POST", editPath+"?return_url=%2Fbookmarks%3Fq%3Dgo", session, form))
	if updated.Code != 302 || updated.Header().Get("Location") != "/bookmarks?q=go" {
		t.Fatalf("updated: %d %q", updated.Code, updated.Header().Get("Location"))
	}
	if err := db.QueryRowContext(ctx, `SELECT title FROM bookmarks_bookmark WHERE id=?`, id).Scan(&title); err != nil || title != "Edited" {
		t.Fatalf("edited bookmark: %q %v", title, err)
	}
}
