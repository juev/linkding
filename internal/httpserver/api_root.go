package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/juev/linkding/internal/auth"
)

type apiRootMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Renders     []string `json:"renders"`
	Parses      []string `json:"parses"`
}

var pinnedAPIRootMetadata = apiRootMetadata{
	Name:        "Api Root",
	Description: "The default basic root view for DefaultRouter",
	Renders:     []string{"application/json", "text/html"},
	Parses:      []string{"application/json", "application/x-www-form-urlencoded", "multipart/form-data"},
}

func serveAPIRoot(w http.ResponseWriter, r *http.Request, path string, users *auth.Repository) {
	if r.URL.Path != path {
		writeNotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Allow", "GET, HEAD, OPTIONS")
	w.Header().Set("Vary", "Accept, Accept-Language, Cookie")
	w.Header().Set("Content-Language", selectedAdminLanguage(r).Code)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")

	token, present, parseErr := auth.ParseTokenAuthorization(r.Header.Get("Authorization"))
	if parseErr != nil {
		w.Header().Set("WWW-Authenticate", "Token")
		writeAPIRootDetail(w, r, http.StatusUnauthorized, parseErr.Error())
		return
	}
	var err error
	if present {
		_, err = users.AuthenticateToken(r.Context(), token)
	} else if cookie, cookieErr := r.Cookie(auth.SessionCookieName); cookieErr == nil {
		_, err = users.AuthenticateSession(r.Context(), cookie.Value)
	} else {
		err = auth.ErrInvalidCredentials
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		w.Header().Set("WWW-Authenticate", "Token")
		if present {
			writeAPIRootDetail(w, r, http.StatusUnauthorized, "Invalid token.")
		} else {
			writeAPIRootDetail(w, r, http.StatusUnauthorized, "Authentication credentials were not provided.")
		}
		return
	}
	if err != nil {
		writeAPIRootDetail(w, r, http.StatusInternalServerError, "Server error")
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_ = writeJSON(w, map[string]any{})
		}
	case http.MethodOptions:
		body, err := json.Marshal(pinnedAPIRootMetadata)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	default:
		writeDetail(w, http.StatusMethodNotAllowed, `Method "`+r.Method+`" not allowed.`)
	}
}

func writeAPIRootDetail(w http.ResponseWriter, r *http.Request, status int, detail string) {
	if r.Method == http.MethodHead {
		detail = localizedAPIDetail(w.Header().Get("Content-Language"), detail)
		body, _ := json.Marshal(map[string]string{"detail": detail})
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		return
	}
	writeDetail(w, status, detail)
}
