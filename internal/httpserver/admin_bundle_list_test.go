package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestAdminBundleListSearchAndOwnerFilter(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db, cfg.DBEngine); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, cfg.DBEngine)
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
		name, search, shared, unread string
		owner                        int64
	}{
		{"Alice bundle", "literal%search", "yes", "no", alice.ID},
		{"Bob bundle", "literal%search", "no", "yes", bob.ID},
		{"Plain bundle", "ordinary", "off", "off", alice.ID},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,"order",date_created,date_modified,owner_id,filter_shared,filter_unread) VALUES (?,?, '', '', '', 0, ?, ?, ?, ?, ?)`, item.name, item.search, time.Now().UTC(), time.Now().UTC(), item.owner, item.shared, item.unread)
		if err != nil {
			t.Fatal(err)
		}
	}
	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/bookmarks/bookmarkbundle/?" + url.Values{"q": {"%"}, "owner__username": {"alice"}}.Encode()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	New(db, cfg, t.TempDir()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bundle list: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Alice bundle") || strings.Contains(body, "Bob bundle") || strings.Contains(body, "Plain bundle") {
		t.Fatalf("search or owner filter selected wrong bundles: %s", body)
	}
	for _, expected := range []string{"Any tags", "All tags", "Excluded tags", "Filter shared", "Filter unread", "Shared", "Read"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing list field or choice label %q: %s", expected, body)
		}
	}
}

func TestAdminBundleListQueryPostgres(t *testing.T) {
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
	user, err := auth.NewRepository(db, "postgres").CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("bundle_search_%d", time.Now().UnixNano()), Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM bookmarks_bookmarkbundle WHERE owner_id = $1`,
			`DELETE FROM bookmarks_userprofile WHERE user_id = $1`,
			`DELETE FROM auth_user WHERE id = $1`,
		} {
			if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
	})
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `INSERT INTO bookmarks_bookmarkbundle(name,search,any_tags,all_tags,excluded_tags,"order",date_created,date_modified,owner_id,filter_shared,filter_unread) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, "Postgres bundle", "literal%search", "", "", "", 0, now, now, user.ID, "yes", "no")
	if err != nil {
		t.Fatal(err)
	}
	query, args := adminBundleListQuery("postgres", "%", user.Username)
	var id int64
	var name, owner, search, anyTags, allTags, excludedTags, shared, unread string
	var order int
	var created time.Time
	if err := db.QueryRowContext(ctx, query, args...).Scan(&id, &name, &owner, &order, &search, &anyTags, &allTags, &excludedTags, &shared, &unread, &created); err != nil {
		t.Fatal(err)
	}
	if name != "Postgres bundle" || owner != user.Username || search != "literal%search" || shared != "Shared" || unread != "Read" || created.IsZero() {
		t.Fatalf("filtered bundle: %d %q %q %q %q %q %v", id, name, owner, search, shared, unread, created)
	}
}
