package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func TestBookmarkAssetUploadListDownloadDelete(t *testing.T) {
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
	alice, err := users.CreateUser(ctx, auth.NewUser{Username: "assets-alice", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := users.CreateUser(ctx, auth.NewUser{Username: "assets-bob", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	repo := bookmarks.NewRepository(db, "sqlite")
	item, _, err := repo.CreateOrUpdateData(ctx, alice.ID, bookmarks.CreateInput{URL: "https://example.com/asset"})
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		key   string
		owner int64
	}{{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", alice.ID}, {"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", bob.ID}} {
		if _, err := db.ExecContext(ctx, "INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (?, 'fixture', ?, ?)", fixture.key, time.Now().UTC(), fixture.owner); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(db, cfg, t.TempDir())
	base := "/api/bookmarks/" + strconv.FormatInt(item.ID, 10) + "/assets/"
	call := func(method, path, token, bodyType string, body *bytes.Buffer) *httptest.ResponseRecorder {
		t.Helper()
		var input *bytes.Buffer
		if body == nil {
			input = new(bytes.Buffer)
		} else {
			input = body
		}
		request := httptest.NewRequest(method, path, input)
		if token != "" {
			request.Header.Set("Authorization", "Token "+token)
		}
		if bodyType != "" {
			request.Header.Set("Content-Type", bodyType)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	aliceToken := strings.Repeat("a", 40)
	bobToken := strings.Repeat("b", 40)
	if got := call(http.MethodGet, base, bobToken, "", nil); got.Code != http.StatusNotFound {
		t.Fatalf("other owner list: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodPost, base+"upload/", aliceToken, "", nil); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "No file provided") {
		t.Fatalf("missing upload: %d %s", got.Code, got.Body.String())
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, err := form.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("asset fixture\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	created := call(http.MethodPost, base+"upload/", aliceToken, form.FormDataContentType(), &upload)
	if created.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", created.Code, created.Body.String())
	}
	var asset apiAsset
	if err := json.Unmarshal(created.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.ID < 1 || asset.Bookmark != item.ID || asset.AssetType != "upload" || asset.DisplayName != "notes.txt" || asset.Status != "complete" || asset.FileSize == nil || *asset.FileSize == 0 {
		t.Fatalf("asset upload response: %+v", asset)
	}
	listed := call(http.MethodGet, base, aliceToken, "", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"count":1`) {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	detailPath := base + strconv.FormatInt(asset.ID, 10) + "/"
	if got := call(http.MethodGet, detailPath, aliceToken, "", nil); got.Code != http.StatusOK || got.Body.String() != created.Body.String() {
		t.Fatalf("detail: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodGet, detailPath, bobToken, "", nil); got.Code != http.StatusNotFound {
		t.Fatalf("other owner detail: %d", got.Code)
	}
	download := call(http.MethodGet, detailPath+"download/", aliceToken, "", nil)
	if download.Code != http.StatusOK || download.Body.String() != "asset fixture\n" || !strings.Contains(download.Header().Get("Content-Disposition"), "notes.txt") {
		t.Fatalf("download: %d %q %q", download.Code, download.Body.String(), download.Header().Get("Content-Disposition"))
	}
	if got := call(http.MethodDelete, detailPath, aliceToken, "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodGet, detailPath, aliceToken, "", nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted detail: %d", got.Code)
	}
	var filename string
	if err := db.QueryRowContext(ctx, "SELECT file FROM bookmarks_bookmarkasset WHERE id = ?", asset.ID).Scan(&filename); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("asset remains: file=%q err=%v", filename, err)
	}
	entries, err := os.ReadDir(filepath.Join(cfg.DataDir, "assets"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("asset files after delete: %v err=%v", entries, err)
	}
	if got := call(http.MethodGet, "/api/bookmarks/singlefile", aliceToken, "", nil); got.Code != http.StatusMovedPermanently || got.Header().Get("Location") != "/api/bookmarks/singlefile/" || got.Body.Len() != 0 {
		t.Fatalf("singlefile path without slash: %d %s", got.Code, got.Body.String())
	}
	var singlefile bytes.Buffer
	snapshotForm := multipart.NewWriter(&singlefile)
	if err := snapshotForm.WriteField("url", item.URL); err != nil {
		t.Fatal(err)
	}
	snapshotPart, err := snapshotForm.CreateFormFile("file", "snapshot.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotPart.Write([]byte("<html>snapshot fixture</html>")); err != nil {
		t.Fatal(err)
	}
	if err := snapshotForm.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := call(http.MethodPost, "/api/bookmarks/singlefile/", aliceToken, snapshotForm.FormDataContentType(), &singlefile)
	if snapshot.Code != http.StatusCreated || snapshot.Body.String() != `{"message":"Snapshot uploaded successfully."}` {
		t.Fatalf("singlefile upload: %d %s", snapshot.Code, snapshot.Body.String())
	}
	var snapshotID int64
	var snapshotName, snapshotStatus string
	if err := db.QueryRowContext(ctx, `SELECT a.id, a.file, a.status FROM bookmarks_bookmarkasset a
		JOIN bookmarks_bookmark b ON b.latest_snapshot_id = a.id WHERE b.id = ?`, item.ID).Scan(&snapshotID, &snapshotName, &snapshotStatus); err != nil {
		t.Fatal(err)
	}
	if snapshotStatus != "complete" || snapshotName == "" {
		t.Fatalf("saved snapshot: %q %q", snapshotStatus, snapshotName)
	}
	snapshotDownload := call(http.MethodGet, base+strconv.FormatInt(snapshotID, 10)+"/download/", aliceToken, "", nil)
	if snapshotDownload.Code != http.StatusOK || snapshotDownload.Body.String() != "<html>snapshot fixture</html>" {
		t.Fatalf("snapshot download: %d %q", snapshotDownload.Code, snapshotDownload.Body.String())
	}
	secondName := "later-snapshot.html"
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "assets", secondName), []byte("later"), 0o600); err != nil {
		t.Fatal(err)
	}
	var secondID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO bookmarks_bookmarkasset
		(date_created, file, file_size, asset_type, content_type, display_name, status, gzip, bookmark_id)
		VALUES (?, ?, 5, 'snapshot', 'text/html', 'Later snapshot', 'complete', 0, ?) RETURNING id`,
		time.Now().UTC().Add(time.Minute), secondName, item.ID).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE bookmarks_bookmark SET latest_snapshot_id = ? WHERE id = ?", secondID, item.ID); err != nil {
		t.Fatal(err)
	}
	if got := call(http.MethodDelete, base+strconv.FormatInt(secondID, 10)+"/", aliceToken, "", nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete latest snapshot: %d %s", got.Code, got.Body.String())
	}
	var latest int64
	if err := db.QueryRowContext(ctx, "SELECT latest_snapshot_id FROM bookmarks_bookmark WHERE id = ?", item.ID).Scan(&latest); err != nil || latest != snapshotID {
		t.Fatalf("fallback snapshot: %d err=%v", latest, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "assets", secondName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted snapshot file remains: %v", err)
	}
	cfg.DisableAssetUpload = true
	disabledHandler := New(db, cfg, t.TempDir())
	request := httptest.NewRequest(http.MethodPost, base+"upload/", nil)
	request.Header.Set("Authorization", "Token "+aliceToken)
	response := httptest.NewRecorder()
	disabledHandler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Body.String() != `{"error":"Asset upload is disabled."}` {
		t.Fatalf("disabled upload: %d %s", response.Code, response.Body.String())
	}
}
