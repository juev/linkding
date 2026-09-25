package httpserver

import (
	"net/url"
	"strings"
	"testing"
)

func TestAdminBookmarkListQuery(t *testing.T) {
	filters := url.Values{
		"owner__username":    {"alice"},
		"is_archived__exact": {"0"},
		"unread__exact":      {"1"},
		"tags__id__exact":    {"42"},
	}
	query, args := adminBookmarkListQuery("sqlite", `"two words"`, filters)
	for _, fragment := range []string{
		"SELECT DISTINCT",
		"u.username = ?",
		"b.is_archived = ?",
		"b.unread = ?",
		"t.id = ?",
		"ld_ci_contains(b.title, ?)",
		"ld_ci_contains(b.description, ?)",
		"ld_ci_contains(b.website_title, ?)",
		"ld_ci_contains(b.website_description, ?)",
		"ld_ci_contains(b.url, ?)",
		"ld_ci_contains(t.name, ?)",
		"ORDER BY b.date_added DESC",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("query missing %q: %s", fragment, query)
		}
	}
	if len(args) != 10 || args[0] != "alice" || args[1] != false || args[2] != true || args[3] != "42" || args[4] != "two words" {
		t.Fatalf("unexpected args: %#v", args)
	}

	postgresQuery, postgresArgs := adminBookmarkListQuery("postgres", "%_\\", nil)
	if !strings.Contains(postgresQuery, `b.title ILIKE $1 ESCAPE '\'`) {
		t.Errorf("postgres search escaping missing: %s", postgresQuery)
	}
	if len(postgresArgs) != 6 || postgresArgs[0] != `%\%\_\\%` {
		t.Errorf("unexpected postgres args: %#v", postgresArgs)
	}
}

func TestAdminBookmarkListFiltersPreserveOtherFilters(t *testing.T) {
	params := url.Values{
		"q":               {"needle"},
		"owner__username": {"alice"},
		"unread__exact":   {"1"},
		"tags__isnull":    {"True"},
		"p":               {"3"},
	}
	groups := adminBookmarkListFilters(params)
	if len(groups) != 4 {
		t.Fatalf("got %d filter groups, want 4", len(groups))
	}
	if got, want := groups[0].Options, 0; len(got) != want {
		t.Fatalf("owner options populated before database read: %d", len(got))
	}
	if groups[0].AllURL != "?q=needle&tags__isnull=True&unread__exact=1" {
		t.Fatalf("owner clear URL = %q", groups[0].AllURL)
	}
	if groups[2].Value != "1" || groups[2].Options[0].URL != "?owner__username=alice&q=needle&tags__isnull=True&unread__exact=1" {
		t.Fatalf("unread filter did not preserve active filters: %#v", groups[2])
	}
	if groups[3].AllURL != "?owner__username=alice&q=needle&unread__exact=1" {
		t.Fatalf("tag clear URL = %q", groups[3].AllURL)
	}
}

func TestAdminBookmarkListFiltersUntagged(t *testing.T) {
	query, args := adminBookmarkListQuery("sqlite", "", url.Values{"tags__isnull": {"True"}})
	if !strings.Contains(query, "t.id IS NULL") || len(args) != 0 {
		t.Fatalf("unexpected untagged query: %s %#v", query, args)
	}
}
