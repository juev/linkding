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

type apiBundle struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Search       string `json:"search"`
	AnyTags      string `json:"any_tags"`
	AllTags      string `json:"all_tags"`
	ExcludedTags string `json:"excluded_tags"`
	FilterUnread string `json:"filter_unread"`
	FilterShared string `json:"filter_shared"`
	Order        int    `json:"order"`
	DateCreated  string `json:"date_created"`
	DateModified string `json:"date_modified"`
}

type bundleInput struct {
	Name         *string `json:"name"`
	Search       *string `json:"search"`
	AnyTags      *string `json:"any_tags"`
	AllTags      *string `json:"all_tags"`
	ExcludedTags *string `json:"excluded_tags"`
	FilterUnread *string `json:"filter_unread"`
	FilterShared *string `json:"filter_shared"`
	Order        *int    `json:"order"`
}

func scanBundle(row interface{ Scan(...any) error }) (apiBundle, error) {
	var bundle apiBundle
	var created, modified time.Time
	err := row.Scan(&bundle.ID, &bundle.Name, &bundle.Search, &bundle.AnyTags, &bundle.AllTags,
		&bundle.ExcludedTags, &bundle.FilterUnread, &bundle.FilterShared, &bundle.Order, &created, &modified)
	if err != nil {
		return apiBundle{}, err
	}
	bundle.DateCreated = created.UTC().Format("2006-01-02T15:04:05.000000Z")
	bundle.DateModified = modified.UTC().Format("2006-01-02T15:04:05.000000Z")
	return bundle, nil
}

func bundleColumns() string {
	return `id, name, search, any_tags, all_tags, excluded_tags, filter_unread,
		filter_shared, "order", date_created, date_modified`
}

func getBundle(r *http.Request, cfg config.Config, db *sql.DB, ownerID, id int64) (apiBundle, error) {
	query := "SELECT " + bundleColumns() + " FROM bookmarks_bookmarkbundle WHERE id = " + assetMarker(cfg.DBEngine, 1) + " AND owner_id = " + assetMarker(cfg.DBEngine, 2)
	return scanBundle(db.QueryRowContext(r.Context(), query, id, ownerID))
}

func serveBundlesAPI(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository) {
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
		w.Header().Set("Allow", "GET, PUT, PATCH, DELETE, HEAD, OPTIONS")
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
			writeAPIMetadata(w, "Bookmark Bundle List", http.MethodPost, bundleAPISchema)
			return
		}
		_, lookupErr := getBundle(r, cfg, db, user.ID, id)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			writeAPIMetadata(w, "Bookmark Bundle Instance", "", "")
			return
		}
		if lookupErr != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		writeAPIMetadata(w, "Bookmark Bundle Instance", http.MethodPut, bundleAPISchema)
		return
	}
	if list && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		serveBundleList(w, r, cfg, db, user.ID)
		return
	}
	if list && r.Method == http.MethodPost {
		if !present && !verifyAPICSRF(r, cfg) {
			writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
			return
		}
		input, ok := decodeBundleInput(w, r, true)
		if !ok {
			return
		}
		bundle, err := createBundle(r, cfg, db, user.ID, input)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = writeJSON(w, bundle)
		return
	}
	if !list && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		bundle, err := getBundle(r, cfg, db, user.ID, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeDetail(w, http.StatusNotFound, "No BookmarkBundle matches the given query.")
			return
		}
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_ = writeJSON(w, bundle)
			}
		case http.MethodDelete:
			if !present && !verifyAPICSRF(r, cfg) {
				writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
				return
			}
			if err := deleteBundle(r, cfg, db, user.ID, id); err != nil {
				writeDetail(w, http.StatusInternalServerError, "Server error")
				return
			}
			w.Header().Del("Content-Type")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPut, http.MethodPatch:
			if !present && !verifyAPICSRF(r, cfg) {
				writeDetail(w, http.StatusForbidden, "CSRF Failed: CSRF token missing or incorrect.")
				return
			}
			input, ok := decodeBundleInput(w, r, r.Method == http.MethodPut)
			if !ok {
				return
			}
			updated, err := updateBundle(r, cfg, db, user.ID, bundle, input)
			if err != nil {
				writeDetail(w, http.StatusInternalServerError, "Server error")
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = writeJSON(w, updated)
		}
		return
	}
	writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
}

func decodeBundleInput(w http.ResponseWriter, r *http.Request, requireName bool) (bundleInput, bool) {
	var input bundleInput
	validation, err := decodeDRFJSONObject(r.Body, &input,
		[]string{"name", "search", "any_tags", "all_tags", "excluded_tags"},
		[]string{"filter_unread", "filter_shared"}, nil)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, drfJSONErrorDetail(err))
		return input, false
	}
	if validation != nil {
		writeFieldError(w, validation.field, validation.message)
		return input, false
	}
	for _, value := range []*string{input.Name, input.Search, input.AnyTags, input.AllTags, input.ExcludedTags, input.FilterUnread, input.FilterShared} {
		if value != nil {
			*value = strings.TrimSpace(*value)
		}
	}
	if requireName && input.Name == nil {
		writeFieldError(w, "name", "This field is required.")
		return input, false
	}
	if input.Name != nil && *input.Name == "" {
		writeFieldError(w, "name", "This field may not be blank.")
		return input, false
	}
	for _, field := range []struct {
		key   string
		value *string
		max   int
	}{{"name", input.Name, 256}, {"search", input.Search, 256}, {"any_tags", input.AnyTags, 1024}, {"all_tags", input.AllTags, 1024}, {"excluded_tags", input.ExcludedTags, 1024}} {
		if field.value != nil && len([]rune(*field.value)) > field.max {
			writeFieldError(w, field.key, "Ensure this field has no more than "+strconv.Itoa(field.max)+" characters.")
			return input, false
		}
	}
	for _, field := range []struct {
		key   string
		value *string
	}{{"filter_unread", input.FilterUnread}, {"filter_shared", input.FilterShared}} {
		if field.value != nil && *field.value != "off" && *field.value != "yes" && *field.value != "no" {
			writeFieldError(w, field.key, "\""+*field.value+"\" is not a valid choice.")
			return input, false
		}
	}
	return input, true
}

func createBundle(r *http.Request, cfg config.Config, db *sql.DB, ownerID int64, input bundleInput) (apiBundle, error) {
	var bundle apiBundle
	bundle.Name = *input.Name
	if input.Search != nil {
		bundle.Search = *input.Search
	}
	if input.AnyTags != nil {
		bundle.AnyTags = *input.AnyTags
	}
	if input.AllTags != nil {
		bundle.AllTags = *input.AllTags
	}
	if input.ExcludedTags != nil {
		bundle.ExcludedTags = *input.ExcludedTags
	}
	bundle.FilterUnread, bundle.FilterShared = "off", "off"
	if input.FilterUnread != nil {
		bundle.FilterUnread = *input.FilterUnread
	}
	if input.FilterShared != nil {
		bundle.FilterShared = *input.FilterShared
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return bundle, err
	}
	defer tx.Rollback()
	if input.Order != nil {
		bundle.Order = *input.Order
	} else {
		query := `SELECT COALESCE(MAX("order"), -1) + 1 FROM bookmarks_bookmarkbundle WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1)
		if err := tx.QueryRowContext(r.Context(), query, ownerID).Scan(&bundle.Order); err != nil {
			return bundle, err
		}
	}
	now := time.Now().UTC()
	markers := make([]string, 12)
	for i := range markers {
		markers[i] = assetMarker(cfg.DBEngine, i+1)
	}
	query := `INSERT INTO bookmarks_bookmarkbundle (name, search, any_tags, all_tags, excluded_tags,
		filter_unread, filter_shared, "order", date_created, date_modified, owner_id)
		VALUES (` + strings.Join(markers[:11], ",") + `) RETURNING id`
	if err := tx.QueryRowContext(r.Context(), query, bundle.Name, bundle.Search, bundle.AnyTags, bundle.AllTags,
		bundle.ExcludedTags, bundle.FilterUnread, bundle.FilterShared, bundle.Order, now, now, ownerID).Scan(&bundle.ID); err != nil {
		return bundle, err
	}
	if err := tx.Commit(); err != nil {
		return bundle, err
	}
	bundle.DateCreated = now.Format("2006-01-02T15:04:05.000000Z")
	bundle.DateModified = bundle.DateCreated
	return bundle, nil
}

func updateBundle(r *http.Request, cfg config.Config, db *sql.DB, ownerID int64, bundle apiBundle, input bundleInput) (apiBundle, error) {
	if input.Name != nil {
		bundle.Name = *input.Name
	}
	if input.Search != nil {
		bundle.Search = *input.Search
	}
	if input.AnyTags != nil {
		bundle.AnyTags = *input.AnyTags
	}
	if input.AllTags != nil {
		bundle.AllTags = *input.AllTags
	}
	if input.ExcludedTags != nil {
		bundle.ExcludedTags = *input.ExcludedTags
	}
	if input.FilterUnread != nil {
		bundle.FilterUnread = *input.FilterUnread
	}
	if input.FilterShared != nil {
		bundle.FilterShared = *input.FilterShared
	}
	if input.Order != nil {
		bundle.Order = *input.Order
	}
	now := time.Now().UTC()
	markers := make([]string, 12)
	for i := range markers {
		markers[i] = assetMarker(cfg.DBEngine, i+1)
	}
	query := `UPDATE bookmarks_bookmarkbundle SET name = ` + markers[0] + `, search = ` + markers[1] +
		`, any_tags = ` + markers[2] + `, all_tags = ` + markers[3] + `, excluded_tags = ` + markers[4] +
		`, filter_unread = ` + markers[5] + `, filter_shared = ` + markers[6] + `, "order" = ` + markers[7] +
		`, date_modified = ` + markers[8] + ` WHERE id = ` + markers[9] + ` AND owner_id = ` + markers[10]
	if _, err := db.ExecContext(r.Context(), query, bundle.Name, bundle.Search, bundle.AnyTags, bundle.AllTags,
		bundle.ExcludedTags, bundle.FilterUnread, bundle.FilterShared, bundle.Order, now, bundle.ID, ownerID); err != nil {
		return bundle, err
	}
	bundle.DateModified = now.Format("2006-01-02T15:04:05.000000Z")
	return bundle, nil
}

func deleteBundle(r *http.Request, cfg config.Config, db *sql.DB, ownerID, id int64) error {
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := "DELETE FROM bookmarks_bookmarkbundle WHERE id = " + assetMarker(cfg.DBEngine, 1) + " AND owner_id = " + assetMarker(cfg.DBEngine, 2)
	if _, err := tx.ExecContext(r.Context(), query, id, ownerID); err != nil {
		return err
	}
	query = `SELECT id FROM bookmarks_bookmarkbundle WHERE owner_id = ` + assetMarker(cfg.DBEngine, 1) + ` ORDER BY "order", id`
	rows, err := tx.QueryContext(r.Context(), query, ownerID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var next int64
		if err := rows.Scan(&next); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, next)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	query = `UPDATE bookmarks_bookmarkbundle SET "order" = ` + assetMarker(cfg.DBEngine, 1) + ` WHERE id = ` + assetMarker(cfg.DBEngine, 2)
	for index, next := range ids {
		if _, err := tx.ExecContext(r.Context(), query, index, next); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func serveBundleList(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, ownerID int64) {
	limit := positiveIntOr(r.URL.Query().Get("limit"), 100)
	offset := nonnegativeIntOr(r.URL.Query().Get("offset"), 0)
	var count int64
	if err := db.QueryRowContext(r.Context(), "SELECT count(*) FROM bookmarks_bookmarkbundle WHERE owner_id = "+assetMarker(cfg.DBEngine, 1), ownerID).Scan(&count); err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	query := "SELECT " + bundleColumns() + " FROM bookmarks_bookmarkbundle WHERE owner_id = " + assetMarker(cfg.DBEngine, 1) +
		` ORDER BY "order", id LIMIT ` + assetMarker(cfg.DBEngine, 2) + ` OFFSET ` + assetMarker(cfg.DBEngine, 3)
	rows, err := db.QueryContext(r.Context(), query, ownerID, limit, offset)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	defer rows.Close()
	results := make([]apiBundle, 0)
	for rows.Next() {
		bundle, err := scanBundle(rows)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		results = append(results, bundle)
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
			Count    int64       `json:"count"`
			Next     *string     `json:"next"`
			Previous *string     `json:"previous"`
			Results  []apiBundle `json:"results"`
		}{count, next, previous, results})
	}
}
