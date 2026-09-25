package httpserver

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/config"
)

func corsMiddleware(next http.Handler, cfg config.Config) http.Handler {
	allowed := map[string]bool{}
	for _, raw := range strings.Split(cfg.CORSAllowedOrigins, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if origin, ok := parseCORSOrigin(raw); ok {
			allowed[origin] = true
		} else {
			log.Printf("Ignoring invalid origin in LD_CORS_ALLOWED_ORIGINS: %q. Expected format: https://host[:port]", raw)
		}
	}
	if len(allowed) == 0 {
		return next
	}
	apiPrefix := cfg.URLPrefix() + "api/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		origin, valid := parseCORSOrigin(r.Header.Get("Origin"))
		allow := valid && allowed[origin]
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			appendVary(w.Header(), "Origin")
			if allow {
				w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			return
		}
		writer := &corsResponseWriter{ResponseWriter: w, requestOrigin: r.Header.Get("Origin"), allow: allow}
		next.ServeHTTP(writer, r)
		writer.apply()
	})
}

func parseCORSOrigin(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.EscapedPath() != "" && u.EscapedPath() != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

type corsResponseWriter struct {
	http.ResponseWriter
	requestOrigin string
	allow         bool
}

func (w *corsResponseWriter) apply() {
	appendVary(w.Header(), "Origin")
	if w.allow {
		w.Header().Set("Access-Control-Allow-Origin", w.requestOrigin)
	}
}

func (w *corsResponseWriter) WriteHeader(status int) {
	w.apply()
	w.ResponseWriter.WriteHeader(status)
}

func (w *corsResponseWriter) Write(body []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(body)
}

func appendVary(header http.Header, name string) {
	for _, item := range strings.Split(header.Get("Vary"), ",") {
		if strings.EqualFold(strings.TrimSpace(item), name) {
			return
		}
	}
	if previous := header.Get("Vary"); previous != "" {
		header.Set("Vary", previous+", "+name)
	} else {
		header.Set("Vary", name)
	}
}
