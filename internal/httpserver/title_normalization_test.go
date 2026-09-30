package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAPIBookmarkNormalizesLongTitles(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) { testAPIBookmarkNormalizesLongTitles(t, engine) })
	}
}

func titleTestFixture(t *testing.T, engine string) (context.Context, *sql.DB, config.Config, auth.User, string) {
	t.Helper()
	ctx := context.Background()
	cfg := config.Config{DBEngine: engine, DataDir: t.TempDir(), DisableBackgroundTasks: true, AllowedInternalHosts: "127.0.0.1"}
	var db *sql.DB
	var err error
	if engine == "postgres" {
		dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
		}
		db, err = sql.Open("pgx", dsn)
	} else {
		db, err = store.Open(ctx, cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, engine).CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("titles_%d", time.Now().UnixNano()), Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := fmt.Sprintf("%040d", time.Now().UnixNano())
	query := `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (` + assetMarker(engine, 1) + `, 'fixture', ` + assetMarker(engine, 2) + `, ` + assetMarker(engine, 3) + `)`
	if _, err := db.ExecContext(ctx, query, token, time.Now().UTC(), user.ID); err != nil {
		t.Fatal(err)
	}
	return ctx, db, cfg, user, token
}

func testAPIBookmarkNormalizesLongTitles(t *testing.T, engine string) {
	ctx, db, cfg, _, token := titleTestFixture(t, engine)
	handler := New(db, cfg, t.TempDir())
	call := func(method, path string, body map[string]any, wantStatus int) map[string]any {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != wantStatus {
			t.Fatalf("%s: status %d, response %s", method, response.Code, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	raw := " \t\x00\x01" + strings.Repeat("界", 513) + "\n "
	want := strings.Repeat("界", 512)
	created := call(http.MethodPost, "/api/bookmarks/?disable_scraping=1", map[string]any{"url": "https://example.com/long-title", "title": raw}, http.StatusCreated)
	id := int64(created["id"].(float64))
	path := "/api/bookmarks/" + strconv.FormatInt(id, 10) + "/"
	if created["title"] != want {
		t.Fatalf("POST title = %q", created["title"])
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		long := call(method, path, map[string]any{"url": "https://example.com/long-title", "title": raw}, http.StatusOK)
		if long["title"] != want {
			t.Fatalf("%s long title = %q", method, long["title"])
		}
		result := call(method, path, map[string]any{"url": "https://example.com/long-title", "title": " \n👩‍💻\x00\t title\r\n "}, http.StatusOK)
		if result["title"] != "👩‍💻 title" {
			t.Fatalf("%s title = %q", method, result["title"])
		}
		var stored string
		if err := db.QueryRowContext(ctx, `SELECT title FROM bookmarks_bookmark WHERE id = `+assetMarker(engine, 1), id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != "👩‍💻 title" {
			t.Fatalf("stored title = %q", stored)
		}
	}
	result := call(http.MethodPatch, path, map[string]any{"description": "new"}, http.StatusOK)
	if result["title"] != "👩‍💻 title" {
		t.Fatalf("omitted title changed: %q", result["title"])
	}
	call(http.MethodPatch, path, map[string]any{"title": nil}, http.StatusBadRequest)
	call(http.MethodPatch, path, map[string]any{"title": true}, http.StatusBadRequest)
}

func TestBookmarkFormAndAdminNormalizeTitles(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			ctx, db, cfg, user, _ := titleTestFixture(t, engine)
			session, err := auth.NewRepository(db, engine).CreateSession(ctx, user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			csrf, err := auth.NewCSRFSecret()
			if err != nil {
				t.Fatal(err)
			}
			handler := New(db, cfg, t.TempDir())
			request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
				r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			raw := " \t\x00" + strings.Repeat("界", 513)
			want := strings.Repeat("界", 512)
			form := url.Values{"url": {"https://example.com/form-title"}, "title": {raw}, "csrfmiddlewaretoken": {csrf}}
			created := request(http.MethodPost, "/bookmarks/new", form)
			if created.Code != http.StatusFound {
				t.Fatalf("form create: %d %s", created.Code, created.Body.String())
			}
			repo := bookmarks.NewRepository(db, engine)
			item, err := repo.FindExisting(ctx, user.ID, form.Get("url"))
			if err != nil || item.Title != want {
				t.Fatalf("form title: %q, %v", item.Title, err)
			}
			editPath := "/bookmarks/" + strconv.FormatInt(item.ID, 10) + "/edit"
			form.Set("title", "\n👩‍💻\x00 \t title ")
			edited := request(http.MethodPost, editPath, form)
			if edited.Code != http.StatusFound {
				t.Fatalf("form edit: %d %s", edited.Code, edited.Body.String())
			}
			item, err = repo.GetByID(ctx, user.ID, item.ID)
			if err != nil || item.Title != "👩‍💻 title" {
				t.Fatalf("edited title: %q, %v", item.Title, err)
			}
			for _, path := range []string{"/bookmarks/new", editPath, "/admin/bookmarks/bookmark/add/"} {
				get := request(http.MethodGet, path, nil)
				if get.Code != http.StatusOK {
					t.Fatalf("GET %s: %d", path, get.Code)
				}
				start := strings.Index(get.Body.String(), `<input type="text" name="title"`)
				if start < 0 {
					t.Fatalf("title input absent: %s", path)
				}
				input := get.Body.String()[start:]
				input = input[:strings.Index(input, ">")]
				if strings.Contains(input, "maxlength") {
					t.Fatalf("title input prevents long paste: %s", input)
				}
			}
			var tagID int64
			query := `INSERT INTO bookmarks_tag (name,date_added,owner_id) VALUES (` + assetMarker(engine, 1) + `,` + assetMarker(engine, 2) + `,` + assetMarker(engine, 3) + `) RETURNING id`
			if err := db.QueryRowContext(ctx, query, "title-test", time.Now().UTC(), user.ID).Scan(&tagID); err != nil {
				t.Fatal(err)
			}
			adminForm := adminBookmarkTestForm(user.ID, tagID, csrf)
			adminForm.Set("title", raw)
			adminForm.Set("website_title", raw)
			adminForm.Set("_save", "Save")
			admin := request(http.MethodPost, "/admin/bookmarks/bookmark/add/", adminForm)
			if admin.Code != http.StatusFound {
				t.Fatalf("admin create: %d %s", admin.Code, admin.Body.String())
			}
			var title, websiteTitle string
			if err := db.QueryRowContext(ctx, `SELECT title,website_title FROM bookmarks_bookmark WHERE owner_id=`+assetMarker(engine, 1)+` AND url=`+assetMarker(engine, 2), user.ID, adminForm.Get("url")).Scan(&title, &websiteTitle); err != nil {
				t.Fatal(err)
			}
			if title != want || websiteTitle != want {
				t.Fatalf("admin titles: %q / %q", title, websiteTitle)
			}
		})
	}
}

func TestScrapingAndSingleFileNormalizeTitles(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<head><title> \n\x01" + strings.Repeat("界", 513) + "\t </title></head>"))
	}))
	defer page.Close()
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			ctx, db, cfg, user, token := titleTestFixture(t, engine)
			handler := New(db, cfg, t.TempDir())
			body, err := json.Marshal(map[string]string{"url": page.URL + "/scrape"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/bookmarks/", bytes.NewReader(body))
			req.Header.Set("Authorization", "Token "+token)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusCreated {
				t.Fatalf("scraped create: %d %s", response.Code, response.Body.String())
			}
			var upload bytes.Buffer
			writer := multipart.NewWriter(&upload)
			if err := writer.WriteField("url", page.URL+"/singlefile"); err != nil {
				t.Fatal(err)
			}
			file, err := writer.CreateFormFile("file", "snapshot.html")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("<html>snapshot</html>")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req = httptest.NewRequest(http.MethodPost, "/api/bookmarks/singlefile/", &upload)
			req.Header.Set("Authorization", "Token "+token)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusCreated {
				t.Fatalf("singlefile: %d %s", response.Code, response.Body.String())
			}
			for _, path := range []string{"/scrape", "/singlefile"} {
				item, err := bookmarks.NewRepository(db, engine).FindExisting(ctx, user.ID, page.URL+path)
				if err != nil || item.Title != strings.Repeat("界", 512) {
					t.Fatalf("%s title: %q, %v", path, item.Title, err)
				}
			}
		})
	}
}
