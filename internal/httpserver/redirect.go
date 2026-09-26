package httpserver

import (
	"net/http"

	"github.com/juev/linkding/internal/config"
)

func writeRedirect(w http.ResponseWriter, r *http.Request, destination string) {
	setDjangoHTMLHeaders(w, r)
	w.Header().Set("Location", destination)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusFound)
}

func redirectToLogin(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	writeRedirect(w, r, cfg.URLPrefix()+"login?next="+djangoURLQuote(r.URL.RequestURI()))
}
