package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestListFilteredSearchGrammarAndFilters(t *testing.T) {
	for _, engine := range []string{"sqlite", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			ctx := context.Background()
			var db *sql.DB
			var err error
			if engine == "postgres" {
				dsn := os.Getenv("LINKDING_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set LINKDING_TEST_POSTGRES_DSN to a disposable database")
				}
				db, err = sql.Open("pgx", dsn)
			} else {
				db, err = store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: filepath.Join(t.TempDir(), "data")})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if err := store.Migrate(ctx, db, engine); err != nil {
				t.Fatal(err)
			}
			user, err := auth.NewRepository(db, engine).CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("search_%d", time.Now().UnixNano()), Password: "password"})
			if err != nil {
				t.Fatal(err)
			}
			if engine == "postgres" {
				t.Cleanup(func() {
					for _, query := range []string{
						"DELETE FROM bookmarks_bookmarkbundle WHERE owner_id = $1",
						"DELETE FROM bookmarks_bookmark_tags WHERE bookmark_id IN (SELECT id FROM bookmarks_bookmark WHERE owner_id = $1)",
						"DELETE FROM bookmarks_bookmark WHERE owner_id = $1",
						"DELETE FROM bookmarks_tag WHERE owner_id = $1",
						"DELETE FROM bookmarks_userprofile WHERE user_id = $1",
						"DELETE FROM auth_user WHERE id = $1",
					} {
						if _, err := db.ExecContext(context.Background(), query, user.ID); err != nil {
							t.Errorf("fixture cleanup: %v", err)
						}
					}
				})
			}
			repo := NewRepository(db, engine)
			stamps := []time.Time{
				time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			}
			fixtures := []CreateInput{
				{URL: "https://example.com/go", Title: "Go language", Notes: "runtime", Unread: true, Shared: true, TagNames: []string{"coding"}, DateAdded: &stamps[0], DateModified: &stamps[0]},
				{URL: "https://example.com/rust", Title: "Rust guide", Unread: false, Shared: false, TagNames: []string{"coding", "systems"}, DateAdded: &stamps[1], DateModified: &stamps[1]},
				{URL: "https://example.com/alpha", Title: "Alpha", Unread: true, Shared: false, DateAdded: &stamps[2], DateModified: &stamps[2]},
			}
			for _, input := range fixtures {
				if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, input); err != nil {
					t.Fatal(err)
				}
			}
			tagNames, err := repo.ListTagNamesForSearch(ctx, user.ID, true, false, ListOptions{Query: "#coding", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(tagNames)
			if !slices.Equal(tagNames, []string{"coding", "systems"}) {
				t.Fatalf("tag cloud must include all matching bookmarks before pagination: %v", tagNames)
			}
			tagNames, err = repo.ListTagNamesForSearch(ctx, user.ID, true, false, ListOptions{Query: "Go", Limit: 1})
			if err != nil || !slices.Equal(tagNames, []string{"coding"}) {
				t.Fatalf("tag cloud must follow search filters: %v, %v", tagNames, err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET enable_sharing = "+repo.marker(1)+", enable_public_sharing = "+repo.marker(2)+" WHERE user_id = "+repo.marker(3), true, true, user.ID); err != nil {
				t.Fatal(err)
			}
			tagNames, err = repo.ListTagNamesForSearch(ctx, 0, false, true, ListOptions{User: user.Username})
			if err != nil || !slices.Equal(tagNames, []string{"coding"}) {
				t.Fatalf("public shared tag cloud: %v, %v", tagNames, err)
			}
			tagNames, err = repo.ListTagNamesForSearch(ctx, 0, false, true, ListOptions{User: "missing"})
			if err != nil || !slices.Equal(tagNames, []string{"coding"}) {
				t.Fatalf("unknown shared owner must fall back to all public shares: %v, %v", tagNames, err)
			}
			owners, err := repo.ListSharedOwnerNames(ctx, 0, false, ListOptions{Query: "Go", User: "missing"})
			if err != nil || !slices.Equal(owners, []string{user.Username}) {
				t.Fatalf("shared owner choices must follow search without the selected user: %v, %v", owners, err)
			}
			owners, err = repo.ListSharedOwnerNames(ctx, 0, false, ListOptions{Query: "nomatch"})
			if err != nil || len(owners) != 0 {
				t.Fatalf("shared owner choices must follow query filters: %v, %v", owners, err)
			}
			assertTitles := func(opts ListOptions, want ...string) {
				t.Helper()
				opts.Limit = 100
				items, count, err := repo.ListFiltered(ctx, user.ID, opts)
				if err != nil {
					t.Fatal(err)
				}
				if count != int64(len(want)) || len(items) != len(want) {
					t.Fatalf("%+v: count=%d items=%v, want=%v", opts, count, items, want)
				}
				for i, item := range items {
					if item.Title != want[i] {
						t.Fatalf("%+v: item %d=%q, want %q", opts, i, item.Title, want[i])
					}
				}
			}
			assertTitles(ListOptions{Query: "(Go or Rust) #coding", Sort: "title_asc"}, "Go language", "Rust guide")
			assertTitles(ListOptions{Query: "#coding not !unread"}, "Rust guide")
			assertTitles(ListOptions{Query: "not (#coding or Alpha)"})
			assertTitles(ListOptions{Query: "Go or"})
			assertTitles(ListOptions{Query: "!unknown or Go"}, "Go language")
			assertTitles(ListOptions{Query: "not !unknown"}, "Alpha", "Rust guide", "Go language")
			assertTitles(ListOptions{Query: "!untagged", Sort: "title_asc"}, "Alpha")
			assertTitles(ListOptions{Shared: "yes", Unread: "yes"}, "Go language")
			assertTitles(ListOptions{AddedSince: "2026-01-15", Sort: "added_asc"}, "Rust guide", "Alpha")
			assertTitles(ListOptions{Query: "#systems", Sort: "title_desc"}, "Rust guide")
			bundleQuery := `INSERT INTO bookmarks_bookmarkbundle
				(name, search, any_tags, all_tags, excluded_tags, "order", date_created, date_modified, owner_id, filter_shared, filter_unread)
				VALUES (` + placeholders(engine, 11) + `) RETURNING id`
			var bundleID int64
			err = db.QueryRowContext(ctx, bundleQuery, "Go bundle", "", "coding", "coding", "systems", 0, time.Now().UTC(), time.Now().UTC(), user.ID, "yes", "yes").Scan(&bundleID)
			if err != nil {
				t.Fatal(err)
			}
			assertTitles(ListOptions{Bundle: fmt.Sprint(bundleID)}, "Go language")
			preview, previewCount, err := repo.ListBundlePreview(ctx, user.ID, PreviewBundle{AnyTags: "coding", AllTags: "coding", ExcludedTags: "systems", FilterShared: "yes", FilterUnread: "yes"}, ListOptions{Limit: 100})
			if err != nil || previewCount != 1 || len(preview) != 1 || preview[0].Title != "Go language" {
				t.Fatalf("unsaved bundle preview: count=%d items=%v err=%v", previewCount, preview, err)
			}
			assertTitles(ListOptions{Bundle: "999999999"}, "Alpha", "Rust guide", "Go language")
			if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET tag_search = "+repo.marker(1)+", legacy_search = "+repo.marker(2)+" WHERE user_id = "+repo.marker(3), "lax", false, user.ID); err != nil {
				t.Fatal(err)
			}
			assertTitles(ListOptions{Query: "coding", Sort: "title_asc"}, "Go language", "Rust guide")
			if _, err := db.ExecContext(ctx, "UPDATE bookmarks_userprofile SET legacy_search = "+repo.marker(1)+" WHERE user_id = "+repo.marker(2), true, user.ID); err != nil {
				t.Fatal(err)
			}
			assertTitles(ListOptions{Query: "Go or Rust"})
			assertTitles(ListOptions{Query: "!unread #coding"}, "Go language")
			for _, input := range []CreateInput{
				{URL: "https://example.com/literal", Title: "100%_match"},
				{URL: "https://example.com/wildcard", Title: "100xxmatch"},
				{URL: "https://example.com/unicode", Title: "Äpfel"},
			} {
				if _, _, err := repo.CreateOrUpdateData(ctx, user.ID, input); err != nil {
					t.Fatal(err)
				}
			}
			assertTitles(ListOptions{Query: "100%_match"}, "100%_match")
			assertTitles(ListOptions{Query: "äpfel"}, "Äpfel")
		})
	}
}
