package httpserver

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkSaveLogsDatabaseErrorWithoutChangingResponse(t *testing.T) {
	cfg := config.Config{DBEngine: "sqlite", DataDir: t.TempDir(), DisableBackgroundTasks: true}
	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	r := httptest.NewRequest(http.MethodPost, "/api/bookmarks/?token=private-query", strings.NewReader(`{"url":"https://example.com","title":"Example"}`))
	r.Header.Set("Authorization", "Token private-header")
	r.Header.Set("Cookie", "private-cookie")
	w := httptest.NewRecorder()
	serveBookmarkCreate(w, r, cfg, auth.User{ID: 1}, bookmarks.NewRepository(db, cfg.DBEngine), nil)
	if w.Code != http.StatusInternalServerError || w.Body.String() != `{"detail":"Server error"}` {
		t.Fatalf("response changed: status=%d body=%q", w.Code, w.Body.String())
	}
	for _, expected := range []string{"POST", "/api/bookmarks/", "database is closed"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("log missing %q: %q", expected, output.String())
		}
	}
	for _, secret := range []string{"private-query", "private-header", "private-cookie", "https://example.com"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("log contains request data %q: %q", secret, output.String())
		}
	}
}
