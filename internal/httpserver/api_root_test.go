package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAPIRootAuthenticationMethodsAndContextPath(t *testing.T) {
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
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "api-root-user", Password: "parity-password"})
	if err != nil {
		t.Fatal(err)
	}
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_apitoken (key,name,created,user_id) VALUES (?, 'fixture', ?, ?)`, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	path := "/linkding/api/"

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Token" {
			t.Errorf("anonymous %s: status=%d authenticate=%q body=%s", method, response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
		}
		if method == http.MethodHead && (response.Body.Len() != 0 || response.Header().Get("Content-Length") != "58") {
			t.Errorf("anonymous HEAD: body=%q length=%q", response.Body.String(), response.Header().Get("Content-Length"))
		}
	}

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("token %s: status=%d body=%s", method, response.Code, response.Body.String())
		}
		if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Allow") != "GET, HEAD, OPTIONS" || response.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("token %s: API headers missing: %v", method, response.Header())
		}
		switch method {
		case http.MethodGet:
			var got map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || len(got) != 0 {
				t.Errorf("GET body = %q, decoded=%#v, err=%v", response.Body.String(), got, err)
			}
		case http.MethodHead:
			if response.Body.Len() != 0 || response.Header().Get("Content-Length") != "2" {
				t.Errorf("HEAD body = %q length=%q", response.Body.String(), response.Header().Get("Content-Length"))
			}
		case http.MethodOptions:
			const expected = `{"name":"Api Root","description":"The default basic root view for DefaultRouter","renders":["application/json","text/html"],"parses":["application/json","application/x-www-form-urlencoded","multipart/form-data"]}`
			if response.Body.String() != expected {
				t.Errorf("OPTIONS body = %q, want %q", response.Body.String(), expected)
			}
		}
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, path, nil)
	sessionRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK || sessionResponse.Body.String() != "{}" {
		t.Errorf("session GET: status=%d body=%q", sessionResponse.Code, sessionResponse.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, path, nil)
	post.Header.Set("Authorization", "Token "+token)
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusMethodNotAllowed || postResponse.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Errorf("POST: status=%d allow=%q body=%s", postResponse.Code, postResponse.Header().Get("Allow"), postResponse.Body.String())
	}

	for _, wrongPath := range []string{"/api/", "/linkding/api/extra/"} {
		request := httptest.NewRequest(http.MethodGet, wrongPath, nil)
		request.Header.Set("Authorization", "Token "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s: status=%d, want 404", wrongPath, response.Code)
		}
	}
}
