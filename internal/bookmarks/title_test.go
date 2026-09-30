package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestNormalizeTitle(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"spaces", " \t\n Hello\r\n\u00a0\u2003world \u0085", "Hello world"},
		{"controls", "\x00A\x01\x7f\u009fB\x00", "AB"},
		{"formatting", "👩‍💻\u200c\u200f café", "👩‍💻\u200c\u200f café"},
		{"only controls", "\x00\t\n\x01", ""},
		{"boundary", strings.Repeat("界", 512), strings.Repeat("界", 512)},
		{"long", strings.Repeat("界", 513), strings.Repeat("界", 512)},
		{"cleanup before limit", strings.Repeat("\x00", 513) + "Title", "Title"},
		{"trim truncated space", strings.Repeat("a", 511) + " b", strings.Repeat("a", 511)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTitle(tc.input)
			if got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > MaxTitleLength {
				t.Fatalf("invalid bounded UTF-8 title: %q", got)
			}
			if NormalizeTitle(got) != got {
				t.Fatal("normalization is not idempotent")
			}
		})
	}
}

func TestRepositoryNormalizesTitlesAtWriteBoundaries(t *testing.T) {
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
				db, err = store.Open(ctx, config.Config{DBEngine: engine, DataDir: t.TempDir()})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := store.Migrate(ctx, db, engine); err != nil {
				t.Fatal(err)
			}
			user, err := auth.NewRepository(db, engine).CreateUser(ctx, auth.NewUser{Username: fmt.Sprintf("title_repo_%d", time.Now().UnixNano()), Password: "password"})
			if err != nil {
				t.Fatal(err)
			}
			repo := NewRepository(db, engine)
			raw := " \t\x00\x01" + strings.Repeat("界", 513)
			want := strings.Repeat("界", 512)
			first, created, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/title-repo", Title: raw})
			if err != nil || !created || first.Title != want {
				t.Fatalf("create: %q, created=%v, err=%v", first.Title, created, err)
			}
			merged, created, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: first.URL, Title: " \n👩‍💻\x00\t title "})
			if err != nil || created || merged.ID != first.ID || merged.Title != "👩‍💻 title" {
				t.Fatalf("duplicate merge: %+v, created=%v, err=%v", merged, created, err)
			}
			updated, err := repo.UpdateData(ctx, user.ID, first.ID, UpdateInput{Title: &raw})
			if err != nil || updated.Title != want {
				t.Fatalf("update: %q, err=%v", updated.Title, err)
			}
			legacy := " legacy\n title "
			if _, err := db.ExecContext(ctx, `UPDATE bookmarks_bookmark SET title=`+repo.marker(1)+` WHERE id=`+repo.marker(2), legacy, first.ID); err != nil {
				t.Fatal(err)
			}
			desc := "new description"
			updated, err = repo.UpdateData(ctx, user.ID, first.ID, UpdateInput{Description: &desc})
			if err != nil || updated.Title != legacy {
				t.Fatalf("omitted title should preserve legacy bytes: %q, err=%v", updated.Title, err)
			}
			empty, _, err := repo.CreateOrUpdateData(ctx, user.ID, CreateInput{URL: "https://example.com/title-metadata"})
			if err != nil {
				t.Fatal(err)
			}
			enhanced, err := repo.EnhanceMetadata(ctx, user.ID, empty.ID, raw, "metadata")
			if err != nil || enhanced.Title != want {
				t.Fatalf("enhance: %q, err=%v", enhanced.Title, err)
			}
			enhanced, err = repo.EnhanceMetadata(ctx, user.ID, first.ID, raw, "metadata")
			if err != nil || enhanced.Title != legacy {
				t.Fatalf("enhance should preserve existing title: %q, err=%v", enhanced.Title, err)
			}
		})
	}
}
