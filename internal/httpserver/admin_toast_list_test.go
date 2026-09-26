package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestSplitAdminSearch(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []string
	}{
		{`first "two words" last`, []string{"first", "two words", "last"}},
		{`"a \"quoted\" phrase"`, []string{`a "quoted" phrase`}},
		{`one 'two words'`, []string{"one", "two words"}},
		{`prefix"two words"suffix`, []string{`prefix"two words"suffix`}},
	} {
		if got := splitAdminSearch(test.input); !slices.Equal(got, test.want) {
			t.Errorf("splitAdminSearch(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestAdminToastListQueryPostgres(t *testing.T) {
	dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "postgres").CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("toast_search_%d", time.Now().UnixNano()), Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM bookmarks_toast WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES ($1,$2,$3,$4)`, "literal%toast", "Postgres notice", false, user.ID); err != nil {
		t.Fatal(err)
	}
	query, args := adminToastListQuery("postgres", "%", user.Username)
	var id int64
	var key, message, owner, acknowledged string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&id, &key, &message, &owner, &acknowledged); err != nil {
		t.Fatal(err)
	}
	if key != "literal%toast" || message != "Postgres notice" || owner != user.Username || acknowledged != "No" {
		t.Fatalf("filtered Toast: %d %q %q %q %q", id, key, message, owner, acknowledged)
	}
}

func TestAdminToastListSearchOwnerFilterAndPagination(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	admin, err := users.CreateUser(ctx, auth.NewUser{Username: "admin", Password: "password", IsStaff: true, IsSuperuser: true})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		key, message string
		owner        int64
	}{
		{"alice-key", "Important update", alice.ID},
		{"alice-percent", "100% complete", alice.ID},
		{"bob-key", "Important update", bob.ID},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES (?,?,?,?)`, item.key, item.message, false, item.owner); err != nil {
			t.Fatal(err)
		}
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	get := func(params url.Values) *httptest.ResponseRecorder {
		path := "/admin/bookmarks/toast/"
		if params != nil {
			path += "?" + params.Encode()
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	combined := get(url.Values{"q": {"important"}, "owner__username": {"alice"}}).Body.String()
	if !strings.Contains(combined, "alice-key") || strings.Contains(combined, "bob-key") || strings.Contains(combined, "alice-percent") {
		t.Fatalf("combined search and owner filter: %s", combined)
	}
	if !strings.Contains(combined, `name="q"`) || !strings.Contains(combined, `owner__username=alice`) {
		t.Fatalf("search and owner controls missing: %s", combined)
	}
	var toastID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM bookmarks_toast WHERE key='alice-key'`).Scan(&toastID); err != nil {
		t.Fatal(err)
	}
	russian := httptest.NewRequest(http.MethodGet, "/admin/bookmarks/toast/?q=important", nil)
	russian.Header.Set("Accept-Language", "ru")
	russian.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	translated := httptest.NewRecorder()
	handler.ServeHTTP(translated, russian)
	if translated.Code != http.StatusOK || !strings.Contains(translated.Body.String(), fmt.Sprintf("Выбрать этот объект, чтобы применить к нему действие - Toast object (%d)", toastID)) {
		t.Fatalf("Russian Toast action label: %d", translated.Code)
	}
	quoted := get(url.Values{"q": {`"Important update"`}}).Body.String()
	if !strings.Contains(quoted, "alice-key") || !strings.Contains(quoted, "bob-key") || strings.Contains(quoted, "alice-percent") {
		t.Fatalf("quoted phrase search: %s", quoted)
	}
	literal := get(url.Values{"q": {"%"}}).Body.String()
	if !strings.Contains(literal, "alice-percent") || strings.Contains(literal, "alice-key") || strings.Contains(literal, "bob-key") {
		t.Fatalf("literal percent search: %s", literal)
	}
	for i := 0; i < 100; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO bookmarks_toast(key,message,acknowledged,owner_id) VALUES (?,?,?,?)`, "alice-extra", "Important", false, alice.ID); err != nil {
			t.Fatal(err)
		}
	}
	paged := get(url.Values{"q": {"important"}, "owner__username": {"alice"}}).Body.String()
	if !strings.Contains(paged, "p=2") || !strings.Contains(paged, "q=important") || !strings.Contains(paged, "owner__username=alice") {
		t.Fatalf("pagination lost search or owner filter: %s", paged)
	}
	second := get(url.Values{"q": {"important"}, "owner__username": {"alice"}, "p": {"2"}}).Body.String()
	if !strings.Contains(second, "alice-key") || strings.Contains(second, "bob-key") {
		t.Fatalf("second filtered page: %s", second)
	}
}
