package httpserver

import (
	"context"
	"html"
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

func TestAdminUserListSearchAndFilters(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET first_name=?,last_name=?,email=?,is_staff=?,is_active=? WHERE id=?`, "Alice", "Jones", "alice@example.test", true, true, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE auth_user SET first_name=?,last_name=?,email=?,is_superuser=?,is_active=? WHERE id=?`, "Robert", "Smith", "bob@example.test", true, false, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_group(name) VALUES (?)`, "Editors"); err != nil {
		t.Fatal(err)
	}
	var groupID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_group WHERE name=?`, "Editors").Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_user_groups(user_id,group_id) VALUES (?,?)`, alice.ID, groupID); err != nil {
		t.Fatal(err)
	}
	filters := url.Values{"is_staff__exact": {"1"}, "groups__id__exact": {strconv.FormatInt(groupID, 10)}}
	query, args := adminUserListQuery("sqlite", `Alice example.test`, filters)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for rows.Next() {
		var id int64
		var username, email, first, last string
		var staff bool
		if err := rows.Scan(&id, &username, &email, &first, &last, &staff); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		found = append(found, username)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(found) != 1 || found[0] != "alice" {
		t.Fatalf("search and user filters matched %v, want [alice]", found)
	}

	session, err := users.CreateSession(ctx, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/admin/auth/user/?q=alice&is_staff__exact=1&groups__id__exact="+strconv.FormatInt(groupID, 10), nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	response := httptest.NewRecorder()
	New(db, cfg, t.TempDir()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("user list: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{"alice@example.test", "By staff status", "By superuser status", "By active", "By groups", "Editors", `name="q" value="alice"`, `name="is_staff__exact" value="1"`, `name="groups__id__exact" value="` + strconv.FormatInt(groupID, 10) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("user list missing %q", want)
		}
	}
	if strings.Contains(body, "bob@example.test") {
		t.Fatal("user list included non-matching user bob")
	}
	filterURL := adminListURL(url.Values{"q": {"alice"}, "is_staff__exact": {"1"}, "groups__id__exact": {strconv.FormatInt(groupID, 10)}}, "is_active__exact", "0")
	if !strings.Contains(body, html.EscapeString(filterURL)) {
		t.Fatalf("filter links do not preserve search and current filters: %s", body)
	}
}

func TestAdminUserListQueryPostgres(t *testing.T) {
	query, args := adminUserListQuery("postgres", `A%_\`, url.Values{
		"is_staff__exact": {"1"}, "is_superuser__exact": {"0"}, "is_active__exact": {"1"}, "groups__id__exact": {"23"},
	})
	if len(args) != 8 || args[0] != true || args[1] != false || args[2] != true || args[3] != "23" || args[4] != `%A\%\_\\%` || args[5] != `%A\%\_\\%` || args[6] != `%A\%\_\\%` || args[7] != `%A\%\_\\%` {
		t.Fatalf("unexpected PostgreSQL query args: %#v", args)
	}
	if !strings.Contains(query, "g.id = $4") || !strings.Contains(query, "u.first_name ILIKE $") || !strings.Contains(query, "u.last_name ILIKE $") || !strings.Contains(query, `ESCAPE '\'`) || !strings.Contains(query, "ORDER BY u.username") {
		t.Fatalf("unexpected PostgreSQL user list query: %s", query)
	}
}
