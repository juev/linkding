package httpserver

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

type apiTag struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	DateAdded string `json:"date_added"`
}

func scanTag(row interface{ Scan(...any) error }) (apiTag, error) {
	var tag apiTag
	var date time.Time
	if err := row.Scan(&tag.ID, &tag.Name, &date); err != nil {
		return apiTag{}, err
	}
	tag.DateAdded = date.UTC().Format("2006-01-02T15:04:05.000000Z")
	return tag, nil
}

func serveTagsAPI(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	part := strings.TrimPrefix(r.URL.Path, root)
	list := part == ""
	var id int64
	if !list {
		if !strings.HasSuffix(part, "/") || strings.Count(part, "/") != 1 {
			http.NotFound(w, r)
			return
		}
		var err error
		id, err = strconv.ParseInt(strings.TrimSuffix(part, "/"), 10, 64)
		if err != nil || id < 1 {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if list {
		w.Header().Set("Allow", "GET, POST, HEAD, OPTIONS")
	} else {
		w.Header().Set("Allow", "GET, DELETE, HEAD, OPTIONS")
	}
	w.Header().Set("Vary", "Accept, Accept-Language, Cookie")
	w.Header().Set("Content-Language", "en")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	token, present, parseErr := auth.ParseTokenAuthorization(r.Header.Get("Authorization"))
	if parseErr != nil {
		w.Header().Set("WWW-Authenticate", "Token")
		writeDetail(w, http.StatusUnauthorized, "Invalid token header.")
		return
	}
	var user auth.User
	var err error
	if present {
		user, err = users.AuthenticateToken(r.Context(), token)
	} else if cookie, cookieErr := r.Cookie(auth.SessionCookieName); cookieErr == nil {
		user, err = users.AuthenticateSession(r.Context(), cookie.Value)
	} else {
		err = auth.ErrInvalidCredentials
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		w.Header().Set("WWW-Authenticate", "Token")
		if present {
			writeDetail(w, http.StatusUnauthorized, "Invalid token.")
		} else {
			writeDetail(w, http.StatusUnauthorized, "Authentication credentials were not provided.")
		}
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	if r.Method == http.MethodOptions {
		if list {
			writeAPIMetadata(w, "Tag List", "POST", tagAPISchema)
		} else {
			writeAPIMetadata(w, "Tag Instance", "", "")
		}
		return
	}
	if list && r.Method == http.MethodPost {
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		var input struct {
			Name *string `json:"name"`
		}
		validation, err := decodeDRFJSONObject(r.Body, &input, []string{"name"}, nil, nil)
		if err != nil {
			writeDetail(w, http.StatusBadRequest, drfJSONErrorDetail(err))
			return
		}
		if validation != nil {
			writeFieldError(w, validation.field, validation.message)
			return
		}
		if input.Name == nil {
			writeFieldError(w, "name", "This field is required.")
			return
		}
		*input.Name = strings.TrimSpace(*input.Name)
		if *input.Name == "" {
			writeFieldError(w, "name", "This field may not be blank.")
			return
		}
		if len([]rune(*input.Name)) > 64 {
			writeFieldError(w, "name", "Ensure this field has no more than 64 characters.")
			return
		}
		tag, err := getOrCreateTag(r, cfg, db, user.ID, *input.Name)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = writeJSON(w, tag)
		return
	}
	if list && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		serveTagList(w, r, cfg, db, user.ID)
		return
	}
	if !list && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodDelete) {
		query := `SELECT id, name, date_added FROM bookmarks_tag WHERE id = ` + assetMarker(cfg.DBEngine, 1) + ` AND owner_id = ` + assetMarker(cfg.DBEngine, 2)
		tag, err := scanTag(db.QueryRowContext(r.Context(), query, id, user.ID))
		if errors.Is(err, sql.ErrNoRows) {
			writeDetail(w, http.StatusNotFound, "No Tag matches the given query.")
			return
		}
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		if r.Method == http.MethodDelete {
			if !present && !verifyAPICSRF(r, cfg) {
				writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
				return
			}
			if err := deleteTag(r, cfg, db, id, user.ID); err != nil {
				writeDetail(w, http.StatusInternalServerError, "Server error")
				return
			}
			w.Header().Del("Content-Type")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_ = writeJSON(w, tag)
		}
		return
	}
	writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
}

func getOrCreateTag(r *http.Request, cfg config.Config, db *sql.DB, ownerID int64, name string) (apiTag, error) {
	query := `SELECT id, name, date_added FROM bookmarks_tag WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1) +
		` AND LOWER(name) = LOWER(` + assetMarker(cfg.DBEngine, 2) + `) ORDER BY id LIMIT 1`
	tag, err := scanTag(db.QueryRowContext(r.Context(), query, ownerID, name))
	if err == nil {
		return tag, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return apiTag{}, err
	}
	now := time.Now().UTC()
	query = `INSERT INTO bookmarks_tag (name, date_added, owner_id) VALUES (` + assetMarker(cfg.DBEngine, 1) + `, ` + assetMarker(cfg.DBEngine, 2) + `, ` + assetMarker(cfg.DBEngine, 3) + `) RETURNING id`
	if err := db.QueryRowContext(r.Context(), query, name, now, ownerID).Scan(&tag.ID); err != nil {
		return apiTag{}, err
	}
	tag.Name = name
	tag.DateAdded = now.Format("2006-01-02T15:04:05.000000Z")
	return tag, nil
}

func serveTagList(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, ownerID int64) {
	limit := positiveIntOr(r.URL.Query().Get("limit"), 100)
	offset := nonnegativeIntOr(r.URL.Query().Get("offset"), 0)
	var count int64
	if err := db.QueryRowContext(r.Context(), "SELECT count(*) FROM bookmarks_tag WHERE owner_id = "+assetMarker(cfg.DBEngine, 1), ownerID).Scan(&count); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	query := `SELECT id, name, date_added FROM bookmarks_tag WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` ORDER BY id LIMIT ` + assetMarker(cfg.DBEngine, 2) + ` OFFSET ` + assetMarker(cfg.DBEngine, 3)
	rows, err := db.QueryContext(r.Context(), query, ownerID, limit, offset)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	defer rows.Close()
	results := make([]apiTag, 0)
	for rows.Next() {
		tag, err := scanTag(rows)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		results = append(results, tag)
	}
	if err := rows.Err(); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	var next, previous *string
	if int64(offset)+int64(limit) < count {
		value := pageURL(r, limit, offset+limit)
		next = &value
	}
	if offset > 0 {
		value := pageURL(r, limit, max(0, offset-limit))
		previous = &value
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_ = writeJSON(w, struct {
			Count    int64    `json:"count"`
			Next     *string  `json:"next"`
			Previous *string  `json:"previous"`
			Results  []apiTag `json:"results"`
		}{count, next, previous, results})
	}
}

func deleteTag(r *http.Request, cfg config.Config, db *sql.DB, tagID, ownerID int64) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := "DELETE FROM bookmarks_bookmark_tags WHERE tag_id = " + assetMarker(cfg.DBEngine, 1)
	if _, err := tx.ExecContext(r.Context(), query, tagID); err != nil {
		return err
	}
	query = "DELETE FROM bookmarks_tag WHERE id = " + assetMarker(cfg.DBEngine, 1) + " AND owner_id = " + assetMarker(cfg.DBEngine, 2)
	if _, err := tx.ExecContext(r.Context(), query, tagID, ownerID); err != nil {
		return err
	}
	return tx.Commit()
}
