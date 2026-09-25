package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"html/template"
	"net/http"
	"regexp"
	"strconv"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

type pageToast struct {
	ID      int64
	Message string
}

var toastReturnURL = regexp.MustCompile(`^/[a-z]+`)
var toastTemplate = template.Must(template.New("toasts").Parse(`<div class="message-list"><form action="{{.Prefix}}toasts/acknowledge?return_url={{.ReturnURL}}" method="post"><input type="hidden" name="csrfmiddlewaretoken" value="{{.CSRFToken}}">{{range .Toasts}}<div class="toast d-flex">{{.Message}}<button type="submit" name="toast" value="{{.ID}}" class="btn btn-clear"></button></div>{{end}}</form></div>`))

func loadToasts(ctx context.Context, db *sql.DB, dialect string, ownerID int64) ([]pageToast, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,message FROM bookmarks_toast WHERE owner_id = `+assetMarker(dialect, 1)+` AND acknowledged = `+assetMarker(dialect, 2)+` ORDER BY id`, ownerID, false)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var toasts []pageToast
	for rows.Next() {
		var toast pageToast
		if err := rows.Scan(&toast.ID, &toast.Message); err != nil {
			return nil, err
		}
		toasts = append(toasts, toast)
	}
	return toasts, rows.Err()
}

func renderPageToasts(ctx context.Context, db *sql.DB, cfg config.Config, ownerID int64, csrfToken, returnURL string) (template.HTML, error) {
	if ownerID == 0 {
		return "", nil
	}
	toasts, err := loadToasts(ctx, db, cfg.DBEngine, ownerID)
	if err != nil || len(toasts) == 0 {
		return "", err
	}
	var output bytes.Buffer
	if err := toastTemplate.Execute(&output, struct {
		Prefix, ReturnURL, CSRFToken string
		Toasts                       []pageToast
	}{cfg.URLPrefix(), returnURL, csrfToken, toasts}); err != nil {
		return "", err
	}
	return template.HTML(output.String()), nil
}

func serveToastAcknowledge(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository) {
	path := cfg.URLPrefix() + "toasts/acknowledge"
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	user, ok := settingsSession(w, r, path, users, cfg)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if !verifyAPICSRF(r, cfg) {
		http.Error(w, "CSRF verification failed", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PostForm.Get("toast"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	result, err := db.ExecContext(r.Context(), `UPDATE bookmarks_toast SET acknowledged = `+assetMarker(cfg.DBEngine, 1)+` WHERE id = `+assetMarker(cfg.DBEngine, 2)+` AND owner_id = `+assetMarker(cfg.DBEngine, 3), true, id, user.ID)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	updated, err := result.RowsAffected()
	if err != nil || updated == 0 {
		http.NotFound(w, r)
		return
	}
	returnURL := r.URL.Query().Get("return_url")
	if !toastReturnURL.MatchString(returnURL) {
		returnURL = cfg.URLPrefix() + "bookmarks"
	}
	http.Redirect(w, r, returnURL, http.StatusFound)
}
