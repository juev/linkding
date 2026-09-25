package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestProfileAndTagOptionsMatchDRFMetadata(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), ContextPath: "linkding/"}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "options", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag (name, date_added, owner_id) VALUES ('existing', ?, ?)`, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	tagFields := `"id":{"type":"integer","required":false,"read_only":true,"label":"ID"},"name":{"type":"string","required":true,"read_only":false,"label":"Name","max_length":64},"date_added":{"type":"datetime","required":false,"read_only":true,"label":"Date added"}`
	tagList := `{"name":"Tag List","description":"","renders":["application/json","text/html"],"parses":["application/json","application/x-www-form-urlencoded","multipart/form-data"],"actions":{"POST":{` + tagFields + `}}}`
	profile := `{"name":"Profile","description":"","renders":["application/json","text/html"],"parses":["application/json","application/x-www-form-urlencoded","multipart/form-data"]}`
	tagDetail := `{"name":"Tag Instance","description":"","renders":["application/json","text/html"],"parses":["application/json","application/x-www-form-urlencoded","multipart/form-data"]}`

	for _, path := range []string{"/linkding/api/user/profile/", "/linkding/api/tags/", "/linkding/api/tags/1/", "/linkding/api/tags/999999/"} {
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Token" {
			t.Errorf("anonymous OPTIONS %s: status=%d authenticate=%q", path, response.Code, response.Header().Get("WWW-Authenticate"))
		}
	}

	for _, tc := range []struct {
		path, want, allow string
	}{
		{"/linkding/api/user/profile/", profile, "GET, HEAD, OPTIONS"},
		{"/linkding/api/tags/", tagList, "GET, POST, HEAD, OPTIONS"},
		{"/linkding/api/tags/1/", tagDetail, "GET, DELETE, HEAD, OPTIONS"},
		{"/linkding/api/tags/999999/", tagDetail, "GET, DELETE, HEAD, OPTIONS"},
	} {
		req := httptest.NewRequest(http.MethodOptions, tc.path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.String() != tc.want {
			t.Errorf("token OPTIONS %s: status=%d body=%s", tc.path, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Allow") != tc.allow {
			t.Errorf("OPTIONS headers %s: %#v", tc.path, response.Header())
		}
	}

	for _, path := range []string{"/linkding/api/user/profile/", "/linkding/api/tags/"} {
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Errorf("session OPTIONS %s: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}
