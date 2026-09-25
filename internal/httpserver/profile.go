package httpserver

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/juev/linkding/internal/auth"
)

func serveProfile(w http.ResponseWriter, r *http.Request, path string, repo *auth.Repository) {
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Allow", "GET, HEAD, OPTIONS")
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
		user, err = repo.AuthenticateToken(r.Context(), token)
	} else if cookie, cookieErr := r.Cookie(auth.SessionCookieName); cookieErr == nil {
		user, err = repo.AuthenticateSession(r.Context(), cookie.Value)
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
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeDetail(w, http.StatusMethodNotAllowed, "Method \""+r.Method+"\" not allowed.")
		return
	}
	profile, err := repo.GetProfile(r.Context(), user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeDetail(w, http.StatusNotFound, "Not found.")
		return
	}
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_ = writeJSON(w, profile)
	}
}

func writeDetail(w http.ResponseWriter, status int, detail string) {
	w.WriteHeader(status)
	_ = writeJSON(w, map[string]string{"detail": detail})
}
