package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkListActiveArchivedSharedAndSearch(t *testing.T) {
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
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET enable_sharing=1,enable_public_sharing=1,items_per_page=1 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	first, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://one.example", Title: "One", Description: "First", Shared: true, TagNames: []string{"Go"}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://two.example", Title: "Two", TagNames: []string{"Other"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetArchived(ctx, alice.ID, second.ID, true); err != nil {
		t.Fatal(err)
	}
	session, err := users.CreateSession(ctx, alice.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(db, cfg, t.TempDir())
	get := func(path string, loggedIn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if loggedIn {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	active := get("/bookmarks", true)
	if active.Code != 200 || !strings.Contains(active.Body.String(), `data-bookmark-id="`+strconv.FormatInt(first.ID, 10)+`"`) || strings.Contains(active.Body.String(), `data-bookmark-id="`+strconv.FormatInt(second.ID, 10)+`"`) {
		t.Fatalf("active list: %d %q", active.Code, active.Body.String())
	}
	sortOptions := []string{`value="added_asc"`, `value="added_desc"`, `value="modified_asc"`, `value="modified_desc"`, `value="title_asc"`, `value="title_desc"`}
	sortSelect := active.Body.String()
	for _, option := range sortOptions {
		index := strings.Index(sortSelect, option)
		if index < 0 {
			t.Fatalf("missing search sort option %s", option)
		}
		sortSelect = sortSelect[index+len(option):]
	}
	if !strings.Contains(active.Body.String(), `<option value="added_desc" selected>Added ↓</option>`) {
		t.Fatal("added descending must remain the default sort")
	}
	if !strings.Contains(active.Body.String(), "details="+strconv.FormatInt(first.ID, 10)) {
		t.Fatal("bookmark View link must include details query")
	}
	if !strings.Contains(active.Body.String(), `href="/bookmarks/`+strconv.FormatInt(first.ID, 10)+`/edit?return_url=/bookmarks"`) {
		t.Fatal("bookmark edit link must preserve the original return URL")
	}
	if !strings.Contains(active.Body.String(), `<ul class="pagination">`) || !strings.Contains(active.Body.String(), `href="/bookmarks?page=1"`) || !strings.Contains(active.Body.String(), `href="#" tabindex="-1">Previous</a>`) || !strings.Contains(active.Body.String(), `href="#" tabindex="-1">Next</a>`) {
		t.Fatal("bookmark list must render the pinned pagination controls")
	}
	beyondLast := get("/bookmarks?page=999", true)
	if beyondLast.Code != 200 || !strings.Contains(beyondLast.Body.String(), `data-bookmark-id="`+strconv.FormatInt(first.ID, 10)+`"`) || !strings.Contains(beyondLast.Body.String(), `page=1" data-turbo-frame="_top">1</a>`) {
		t.Fatalf("page beyond last must show the last page: %d %q", beyondLast.Code, beyondLast.Body.String())
	}
	ownerDetails := get("/bookmarks?details="+strconv.FormatInt(first.ID, 10), true)
	if ownerDetails.Code != 200 || !strings.Contains(ownerDetails.Body.String(), `<title>Bookmark details - Linkding</title>`) || !strings.Contains(ownerDetails.Body.String(), `class="modal active bookmark-details"`) || !strings.Contains(ownerDetails.Body.String(), `name="update_state"`) {
		t.Fatalf("owner details: %d %q", ownerDetails.Code, ownerDetails.Body.String())
	}
	frameRequest := httptest.NewRequest(http.MethodGet, "/bookmarks?details="+strconv.FormatInt(first.ID, 10), nil)
	frameRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	frameRequest.Header.Set("Turbo-Frame", "details-modal")
	frame := httptest.NewRecorder()
	handler.ServeHTTP(frame, frameRequest)
	if frame.Code != 200 || !strings.HasPrefix(frame.Body.String(), `<html lang="en">`) || !strings.Contains(frame.Body.String(), `<title>Bookmark details - Linkding</title>`) || !strings.Contains(frame.Body.String(), `<turbo-frame id="details-modal"`) {
		t.Fatalf("details frame: %d %q", frame.Code, frame.Body.String())
	}
	archived := get("/bookmarks/archived", true)
	if archived.Code != 200 || !strings.Contains(archived.Body.String(), "Two") || strings.Contains(archived.Body.String(), `data-bookmark-id="`+strconv.FormatInt(first.ID, 10)+`"`) {
		t.Fatalf("archive list: %d %q", archived.Code, archived.Body.String())
	}
	guest := get("/bookmarks/shared", false)
	if guest.Code != 200 || !strings.Contains(guest.Body.String(), "One") || strings.Contains(guest.Body.String(), "Two") || !strings.Contains(guest.Body.String(), "Login") {
		t.Fatalf("guest shared list: %d %q", guest.Code, guest.Body.String())
	}
	if !strings.Contains(guest.Body.String(), `details=`+strconv.FormatInt(first.ID, 10)+`" class="view-action"`) ||
		!strings.Contains(guest.Body.String(), `data-turbo-frame="details-modal">View</a>`) {
		t.Fatal("guest shared bookmark must show the details action")
	}
	if strings.Contains(guest.Body.String(), `id="search-shared-label"`) || strings.Contains(guest.Body.String(), `id="search-unread-label"`) {
		t.Fatal("shared search preferences expose sort only")
	}
	if !strings.Contains(guest.Body.String(), `id="user-heading">User</h2>`) ||
		!strings.Contains(guest.Body.String(), `<option value="" selected>Everyone</option>`) ||
		!strings.Contains(guest.Body.String(), `<option value="alice">alice</option>`) {
		t.Fatal("shared sidebar must offer matching owners")
	}
	guestDetails := get("/bookmarks/shared?details="+strconv.FormatInt(first.ID, 10), false)
	if guestDetails.Code != 200 || !strings.Contains(guestDetails.Body.String(), `class="modal active bookmark-details"`) || strings.Contains(guestDetails.Body.String(), `name="is_archived"`) {
		t.Fatalf("guest shared details: %d %q", guestDetails.Code, guestDetails.Body.String())
	}
	if !strings.Contains(guestDetails.Body.String(), `href="/bookmarks?q=%23Go"`) {
		t.Fatalf("details tag link is not navigable: %q", guestDetails.Body.String())
	}
	privateDetails := get("/bookmarks/shared?details="+strconv.FormatInt(second.ID, 10), false)
	if privateDetails.Code != 200 || strings.Contains(privateDetails.Body.String(), `class="modal active bookmark-details"`) {
		t.Fatalf("guest private details: %d", privateDetails.Code)
	}
	if _, err := db.ExecContext(ctx, `UPDATE bookmarks_userprofile SET enable_sharing=0 WHERE user_id=?`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if got := get("/bookmarks/shared", false); strings.Contains(got.Body.String(), `data-bookmark-id="`+strconv.FormatInt(first.ID, 10)+`"`) {
		t.Fatal("public bookmark should be absent from shared list when sharing is disabled")
	}
	if got := get("/bookmarks/shared?details="+strconv.FormatInt(first.ID, 10), false); !strings.Contains(got.Body.String(), `class="modal active bookmark-details"`) {
		t.Fatal("upstream still permits direct public bookmark reads")
	}
	search := get("/bookmarks?q=nomatch", true)
	if search.Code != 200 || !strings.Contains(search.Body.String(), "You have no bookmarks yet") {
		t.Fatalf("empty search: %d %q", search.Code, search.Body.String())
	}
}
