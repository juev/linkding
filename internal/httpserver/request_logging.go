package httpserver

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/juev/linkding/internal/config"
)

func requestLoggingMiddleware(next http.Handler, cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &loggedResponseWriter{ResponseWriter: w}
		completed := false
		defer func() {
			status := response.status
			if status == 0 {
				status = http.StatusOK
				if !completed {
					// Leave panic handling to net/http. Never write a response here.
					status = http.StatusInternalServerError
				}
			}
			if cfg.DisableRequestLogs && status < http.StatusBadRequest && completed {
				return
			}
			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(clientIP); err == nil {
				clientIP = host
			}
			if cfg.LogXForwardedFor {
				if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
					clientIP = forwarded
				}
			}
			// Exclude query strings, headers and bodies, which can contain tokens.
			log.Printf("request method=%q path=%q status=%d duration=%s client_ip=%q bytes=%d aborted=%t", r.Method, safeRequestPath(r.URL.Path), status, time.Since(started), clientIP, response.bytes, !completed)
		}()
		next.ServeHTTP(response.wrap(), r)
		completed = true
	})
}

func safeRequestPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment == "feeds" && i+1 < len(segments) && segments[i+1] != "" {
			if segments[i+1] != "shared" || i+2 < len(segments) {
				segments[i+1] = "[redacted]"
			}
			break
		}
	}
	return strings.Join(segments, "/")
}

type loggedResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggedResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *loggedResponseWriter) WriteHeader(status int) {
	if w.status == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

type loggingFlusher struct{ writer *loggedResponseWriter }

func (f loggingFlusher) Flush() {
	if f.writer.status == 0 {
		f.writer.status = http.StatusOK
	}
	f.writer.ResponseWriter.(http.Flusher).Flush()
}

// Preserve optional interfaces for handlers that use direct type assertions,
// while Unwrap lets ResponseController reach the underlying writer as well.
func (w *loggedResponseWriter) wrap() http.ResponseWriter {
	_, flush := w.ResponseWriter.(http.Flusher)
	hijacker, hijack := w.ResponseWriter.(http.Hijacker)
	pusher, push := w.ResponseWriter.(http.Pusher)
	flusher := loggingFlusher{writer: w}
	switch {
	case flush && hijack && push:
		return struct {
			*loggedResponseWriter
			http.Flusher
			http.Hijacker
			http.Pusher
		}{w, flusher, hijacker, pusher}
	case flush && hijack:
		return struct {
			*loggedResponseWriter
			http.Flusher
			http.Hijacker
		}{w, flusher, hijacker}
	case flush && push:
		return struct {
			*loggedResponseWriter
			http.Flusher
			http.Pusher
		}{w, flusher, pusher}
	case hijack && push:
		return struct {
			*loggedResponseWriter
			http.Hijacker
			http.Pusher
		}{w, hijacker, pusher}
	case flush:
		return struct {
			*loggedResponseWriter
			http.Flusher
		}{w, flusher}
	case hijack:
		return struct {
			*loggedResponseWriter
			http.Hijacker
		}{w, hijacker}
	case push:
		return struct {
			*loggedResponseWriter
			http.Pusher
		}{w, pusher}
	default:
		return w
	}
}
