package settings

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestGlobalSettingsRequireSuperuserAndValidateGuest(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(db, "sqlite").CreateUser(ctx, auth.NewUser{Username: "guest", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"landing_page": {"shared_bookmarks"}, "guest_profile_user": {strconv.FormatInt(user.ID, 10)}, "enable_link_prefetch": {"on"}}
	if err := UpdateGlobal(ctx, db, "sqlite", false, form); err == nil {
		t.Fatal("ordinary user changed global settings")
	}
	if err := UpdateGlobal(ctx, db, "sqlite", true, form); err != nil {
		t.Fatal(err)
	}
	actual, err := LoadGlobal(ctx, db)
	if err != nil || actual.LandingPage != "shared_bookmarks" || !actual.GuestProfileUserID.Valid || actual.GuestProfileUserID.Int64 != user.ID || !actual.EnableLinkPrefetch {
		t.Fatalf("global settings: %+v err=%v", actual, err)
	}
	form.Set("guest_profile_user", "999999")
	var validation ValidationError
	if err := UpdateGlobal(ctx, db, "sqlite", true, form); !errors.As(err, &validation) || validation.Field != "guest_profile_user" {
		t.Fatalf("unknown guest profile accepted: %v", err)
	}
	form.Set("guest_profile_user", "")
	form.Del("enable_link_prefetch")
	form.Set("landing_page", "login")
	if err := UpdateGlobal(ctx, db, "sqlite", true, form); err != nil {
		t.Fatal(err)
	}
	actual, err = LoadGlobal(ctx, db)
	if err != nil || actual.LandingPage != "login" || actual.GuestProfileUserID.Valid || actual.EnableLinkPrefetch {
		t.Fatalf("cleared global settings: %+v err=%v", actual, err)
	}
}
