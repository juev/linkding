package httpserver

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
)

func serveBookmarkDelete(w http.ResponseWriter, r *http.Request, cfg config.Config, user auth.User, repo *bookmarks.Repository, id int64) {
	files, err := repo.DeleteData(r.Context(), user.ID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeDetail(w, http.StatusNotFound, "No Bookmark matches the given query.")
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	removeStoredFile(filepath.Join(cfg.DataDir, "previews"), files.Preview)
	for _, name := range files.Assets {
		removeStoredFile(filepath.Join(cfg.DataDir, "assets"), name)
	}
	w.Header().Del("Content-Type")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusNoContent)
}

func removeStoredFile(directory, name string) {
	if name == "" {
		return
	}
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("open stored file directory: %v", err)
		return
	}
	defer root.Close()
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("remove stored file %q: %v", name, err)
	}
}
