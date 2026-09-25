package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestFeedsTokenPublicFiltersAndRSSXML(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir()}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	users := auth.NewRepository(db, "sqlite")
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "feed-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "feed-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_sharing = 1, enable_public_sharing = 1 WHERE user_id = ?", bob.ID); err != nil {
		t.Fatal(err)
	}
	const token = "dddddddddddddddddddddddddddddddddddddddd"
	if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_feedtoken (key, created, user_id) VALUES (?, ?, ?)", token, time.Now().UTC(), alice.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	stamp := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		owner int64
		input bookmarks.CreateInput
	}{
		{alice.ID, bookmarks.CreateInput{URL: "https://example.com/a?x=1&y=2", Title: "A & B", Description: "Desc <test>\x00", Unread: true, TagNames: []string{"Go"}, DateAdded: &stamp}},
		{alice.ID, bookmarks.CreateInput{URL: "https://example.com/b", Title: "B", DateAdded: &stamp}},
		{bob.ID, bookmarks.CreateInput{URL: "https://example.com/shared", Title: "Shared", Shared: true, DateAdded: &stamp}},
	} {
		if _, _, err := repo.CreateOrUpdateData(ctx, fixture.owner, fixture.input); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(db, cfg, t.TempDir())
	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	base := "/feeds/" + token
	all := call(base + "/all?q=%23Go")
	if all.Code != http.StatusOK || all.Header().Get("Content-Type") != "application/rss+xml; charset=utf-8" || all.Header().Get("Last-Modified") != "Fri, 25 Sep 2026 09:00:00 GMT" {
		t.Fatalf("all feed: status=%d headers=%v", all.Code, all.Header())
	}
	for _, part := range []string{`<title>A &amp; B</title>`, `<description>Desc &lt;test&gt;</description>`, `<category>Go</category>`, `<guid>https://example.com/a?x=1&amp;y=2</guid>`} {
		if !strings.Contains(all.Body.String(), part) {
			t.Fatalf("missing RSS fragment %q in %s", part, all.Body.String())
		}
	}
	if strings.Contains(all.Body.String(), "\x00") {
		t.Fatal("control character in feed XML")
	}
	unread := call(base + "/unread")
	if unread.Code != http.StatusOK || strings.Count(unread.Body.String(), "<item>") != 1 {
		t.Fatalf("unread feed: %d %s", unread.Code, unread.Body.String())
	}
	shared := call(base + "/shared?user=feed-bob")
	if shared.Code != http.StatusOK || !strings.Contains(shared.Body.String(), "https://example.com/shared") {
		t.Fatalf("shared feed: %d %s", shared.Code, shared.Body.String())
	}
	if missing := call(base + "/shared?user=missing"); missing.Code != http.StatusOK || strings.Contains(missing.Body.String(), "<item>") {
		t.Fatalf("unknown owner feed: %d %s", missing.Code, missing.Body.String())
	}
	public := call("/feeds/shared")
	if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), "https://example.com/shared") || strings.Contains(public.Body.String(), "https://example.com/a") {
		t.Fatalf("public feed: %d %s", public.Code, public.Body.String())
	}
	if invalid := call("/feeds/no-such-token/all"); invalid.Code != http.StatusNotFound {
		t.Fatalf("invalid token: %d", invalid.Code)
	}
}
