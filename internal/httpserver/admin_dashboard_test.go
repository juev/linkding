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

func TestAdminDashboardGroupsModelsShowsActionsAndHandlesHeaderRoutes(t *testing.T) {
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
	user, err := users.CreateUser(ctx, auth.NewUser{Username: "dashboard", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := auth.NewCSRFSecret()
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(ctx, `INSERT INTO bookmarks_tag(name,date_added,owner_id) VALUES (?,?,?)`, "dashboard-tag", time.Now().UTC(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	tagID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAdminLog(ctx, tx, "sqlite", user.ID, "bookmarks", "tag", strconv.FormatInt(tagID, 10), "dashboard-tag", 1, "[]"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: secret})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	page := request(http.MethodGet, "/admin/", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK ||
		!strings.Contains(body, `href="/admin/auth/" class="section"`) ||
		!strings.Contains(body, `href="/admin/bookmarks/" class="section"`) ||
		!strings.Contains(body, `id="recent-actions-module"`) ||
		!strings.Contains(body, `href="/admin/bookmarks/tag/`+strconv.FormatInt(tagID, 10)+`/change/"`) ||
		!strings.Contains(body, `action="/admin/logout/"`) ||
		!strings.Contains(body, `href="/admin/password_change/"`) {
		t.Fatalf("admin dashboard: status=%d body=%q", page.Code, body)
	}
	ruRequest := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	ruRequest.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	ruRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	ruPage := httptest.NewRecorder()
	handler.ServeHTTP(ruPage, ruRequest)
	ruBody := ruPage.Body.String()
	if ruPage.Code != http.StatusOK || ruPage.Header().Get("Content-Language") != "ru" || !strings.Contains(ruBody, `<html lang="ru" dir="ltr">`) || !strings.Contains(ruBody, `<title>Администрирование сайта | linkding Admin</title>`) || !strings.Contains(ruBody, "Добро пожаловать, <strong>dashboard</strong>") || !strings.Contains(ruBody, `title="Модели в приложении Bookmarks"`) || !strings.Contains(ruBody, "Имя модели") || !strings.Contains(ruBody, "Пользователи") || !strings.Contains(ruBody, "Последние действия") {
		t.Fatalf("Russian admin dashboard: status=%d body=%q", ruPage.Code, ruBody)
	}
	for _, tc := range []struct{ path, title string }{
		{"/admin/auth/", "Authentication and Authorization administration"},
		{"/admin/bookmarks/", "Bookmarks administration"},
	} {
		got := request(http.MethodGet, tc.path, nil)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), tc.title) || strings.Contains(got.Body.String(), `id="recent-actions-module"`) {
			t.Fatalf("app index %s: status=%d", tc.path, got.Code)
		}
	}
	for _, tc := range []struct{ path, title string }{
		{"/admin/auth/", "Администрирование приложения «Пользователи и группы»"},
		{"/admin/bookmarks/", "Администрирование приложения «Bookmarks»"},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Accept-Language", "ru")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		got := httptest.NewRecorder()
		handler.ServeHTTP(got, r)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), tc.title) {
			t.Fatalf("Russian app index %s: status=%d body=%q", tc.path, got.Code, got.Body.String())
		}
	}
	list := request(http.MethodGet, "/admin/bookmarks/tag/", nil)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `id="nav-sidebar"`) ||
		!strings.Contains(list.Body.String(), `href="/admin/bookmarks/">Bookmarks</a> &rsaquo; Tags`) ||
		!strings.Contains(list.Body.String(), `Select tag to change`) ||
		!strings.Contains(list.Body.String(), `1 tag</nav>`) {
		t.Fatalf("admin model list navigation: status=%d", list.Code)
	}
	ruListRequest := httptest.NewRequest(http.MethodGet, "/admin/bookmarks/tag/", nil)
	ruListRequest.Header.Set("Accept-Language", "ru")
	ruListRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	ruList := httptest.NewRecorder()
	handler.ServeHTTP(ruList, ruListRequest)
	for _, want := range []string{
		"Выберите tag для изменения", "Хлебные крошки", "Переключить навигацию",
		"Фильтр элементов навигации", "Добавить tag", "Искать tags", "Фильтр", "имя пользователя",
		`alt="Search"`,
		"Выбрано 0 объектов из 1", "Выбрать этот объект, чтобы применить к нему действие - dashboard-tag",
		"Паджинация tags", "Удалить из сортировки", "Сортировать в другом направлении",
	} {
		if ruList.Code != http.StatusOK || !strings.Contains(ruList.Body.String(), want) {
			t.Fatalf("Russian admin tag list missing %q: status=%d", want, ruList.Code)
		}
	}
	password := request(http.MethodGet, "/admin/password_change/", nil)
	if password.Code != http.StatusOK || !strings.Contains(password.Body.String(), `action="/change-password/"`) {
		t.Fatalf("admin password alias: status=%d", password.Code)
	}
	logout := request(http.MethodPost, "/admin/logout/", url.Values{"csrfmiddlewaretoken": {secret}})
	if logout.Code != http.StatusFound || logout.Header().Get("Location") != "/login" {
		t.Fatalf("admin logout: status=%d location=%q", logout.Code, logout.Header().Get("Location"))
	}
	if got := request(http.MethodGet, "/admin/", nil); got.Code != http.StatusFound {
		t.Fatalf("session survived admin logout: status=%d", got.Code)
	}
}
