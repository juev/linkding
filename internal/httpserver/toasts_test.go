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

func TestToastAcknowledgeOwnerCSRFAndReturnURL(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
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
	for _, owner := range []int64{alice.ID, bob.ID} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES (?,?,?,?)`, "hint", "<b>Important</b>", false, owner); err != nil {
			t.Fatal(err)
		}
	}
	key, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: key})
		if withCSRF {
			r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/bookmarks", "/bookmarks/new", "/tags", "/bundles", "/settings/general", "/settings/integrations", "/change-password/"} {
		page := request(http.MethodGet, path, nil, false)
		body := page.Body.String()
		if page.Code != 200 || !strings.Contains(body, "&lt;b&gt;Important&lt;/b&gt;") || strings.Contains(body, "<b>Important</b>") || !strings.Contains(body, "name=\"toast\" value=\"1\"") {
			t.Fatalf("toast display at %s: %d", path, page.Code)
		}
	}
	post := func(id int64, withCSRF bool, returnURL string) *httptest.ResponseRecorder {
		form := url.Values{"toast": {strconv.FormatInt(id, 10)}, "csrfmiddlewaretoken": {csrf}}
		return request(http.MethodPost, "/toasts/acknowledge?return_url="+url.QueryEscape(returnURL), form, withCSRF)
	}
	if got := post(1, false, "/bookmarks"); got.Code != 403 {
		t.Fatalf("missing CSRF: %d", got.Code)
	}
	if got := post(2, true, "/bookmarks"); got.Code != 404 {
		t.Fatalf("other owner toast: %d", got.Code)
	}
	if got := post(1, true, "//evil.example"); got.Code != 302 || got.Header().Get("Location") != "/bookmarks" {
		t.Fatalf("unsafe return URL: %d %q", got.Code, got.Header().Get("Location"))
	}
	var acknowledged bool
	if err := db.QueryRowContext(ctx, `SELECT acknowledged FROM bookmarks_toast WHERE id=1`).Scan(&acknowledged); err != nil || !acknowledged {
		t.Fatalf("toast was not acknowledged: %t %v", acknowledged, err)
	}
	page := request(http.MethodGet, "/bookmarks", nil, false)
	if strings.Contains(page.Body.String(), "Important") {
		t.Fatal("acknowledged toast remains visible")
	}
}
