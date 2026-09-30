package httpserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/config"
	"github.com/juev/linkding/internal/store"
)

func captureRequestLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func TestRequestLogsStatusAndExcludeSecrets(t *testing.T) {
	output := captureRequestLogs(t)
	for _, tc := range []struct {
		name    string
		status  int
		handler http.HandlerFunc
	}{
		{"implicit", 200, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }},
		{"empty", 200, func(w http.ResponseWriter, r *http.Request) {}},
		{"redirect", 302, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login/", 302) }},
		{"error", 500, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 500) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.Reset()
			r := httptest.NewRequest("POST", "/api/bookmarks/?token=query-secret", strings.NewReader("body-secret"))
			r.Header.Set("Authorization", "Bearer header-secret")
			r.Header.Set("Cookie", "session=cookie-secret")
			r.RemoteAddr = "192.0.2.1:1234"
			requestLoggingMiddleware(tc.handler, config.Config{}).ServeHTTP(httptest.NewRecorder(), r)
			entry := output.String()
			for _, field := range []string{"method=\"POST\"", "path=\"/api/bookmarks/\"", "duration=", "client_ip=\"192.0.2.1\""} {
				if !strings.Contains(entry, field) {
					t.Errorf("missing %q in %s", field, entry)
				}
			}
			if !strings.Contains(entry, "status="+strconv.Itoa(tc.status)) || strings.Contains(entry, "secret") {
				t.Fatalf("unexpected log: %s", entry)
			}
		})
	}
}

func TestRequestLogsDisabledStillReportErrors(t *testing.T) {
	output := captureRequestLogs(t)
	for _, status := range []int{200, 204, 302, 400, 404, 500} {
		output.Reset()
		handler := requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }), config.Config{DisableRequestLogs: true})
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		if (output.Len() > 0) != (status >= 400) {
			t.Fatalf("status %d log=%q", status, output.String())
		}
	}
}

func TestRequestLogForwardedAddressOptInAndFeedRedaction(t *testing.T) {
	output := captureRequestLogs(t)
	for _, enabled := range []bool{false, true} {
		for _, path := range []string{"/feeds/feed-secret/all", "/linkding/feeds/feed-secret/unread", "/feeds/feed-secret"} {
			output.Reset()
			r := httptest.NewRequest("GET", path, nil)
			r.RemoteAddr = "192.0.2.1:1234"
			r.Header.Set("X-Forwarded-For", "203.0.113.2, 192.0.2.9")
			requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), config.Config{LogXForwardedFor: enabled}).ServeHTTP(httptest.NewRecorder(), r)
			entry := output.String()
			if strings.Contains(entry, "feed-secret") || !strings.Contains(entry, "[redacted]") {
				t.Fatalf("feed token leaked: %s", entry)
			}
			if strings.Contains(entry, "203.0.113.2") != enabled {
				t.Fatalf("forwarded address enabled=%v: %s", enabled, entry)
			}
		}
	}
	if got := safeRequestPath("/feeds/shared"); got != "/feeds/shared" {
		t.Fatalf("public feed path=%s", got)
	}
}

func TestRequestLogPanicDoesNotWriteResponseOrSwallowPanic(t *testing.T) {
	output := captureRequestLogs(t)
	writer := &capabilityWriter{header: make(http.Header)}
	func() {
		defer func() {
			if recover() != "boom" {
				t.Error("panic was not preserved")
			}
		}()
		requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") }), config.Config{DisableRequestLogs: true}).ServeHTTP(writer, httptest.NewRequest("GET", "/", nil))
	}()
	if writer.status != 0 || !strings.Contains(output.String(), "status=500") {
		t.Fatalf("status=%d log=%q", writer.status, output.String())
	}
}

type capabilityWriter struct {
	header   http.Header
	status   int
	flushed  bool
	hijacked bool
	pushed   bool
}

func TestRequestLogPanicAfterHeadersPreservesSentStatus(t *testing.T) {
	output := captureRequestLogs(t)
	writer := &capabilityWriter{header: make(http.Header)}
	func() {
		defer func() {
			if recover() != "boom" {
				t.Error("panic was not preserved")
			}
		}()
		requestLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			panic("boom")
		}), config.Config{DisableRequestLogs: true}).ServeHTTP(writer, httptest.NewRequest("GET", "/", nil))
	}()
	if writer.status != http.StatusOK || !strings.Contains(output.String(), "status=200") || !strings.Contains(output.String(), "aborted=true") {
		t.Fatalf("status=%d log=%q", writer.status, output.String())
	}
}

func (w *capabilityWriter) Header() http.Header { return w.header }
func (w *capabilityWriter) WriteHeader(status int) {
	if status >= 200 || status == 101 {
		w.status = status
	}
}
func (w *capabilityWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(data), nil
}
func (w *capabilityWriter) Flush() {
	if w.status == 0 {
		w.status = 200
	}
	w.flushed = true
}
func (w *capabilityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, errors.New("test hijack")
}
func (w *capabilityWriter) Push(string, *http.PushOptions) error { w.pushed = true; return nil }

func TestLoggedResponseWriterPreservesCapabilitiesAndStatus(t *testing.T) {
	w := &capabilityWriter{header: make(http.Header)}
	recorder := &loggedResponseWriter{ResponseWriter: w}
	wrapped := recorder.wrap()
	wrapped.WriteHeader(103)
	if recorder.status != 0 {
		t.Fatal("informational status recorded as final")
	}
	wrapped.WriteHeader(201)
	wrapped.(http.Flusher).Flush()
	_, _, _ = wrapped.(http.Hijacker).Hijack()
	_ = wrapped.(http.Pusher).Push("/asset", nil)
	if recorder.status != 201 || !w.flushed || !w.hijacked || !w.pushed {
		t.Fatalf("writer=%+v recorder=%+v", w, recorder)
	}
	if wrapped.(interface{ Unwrap() http.ResponseWriter }).Unwrap() != w {
		t.Fatal("underlying writer not available")
	}
	plain := &loggedResponseWriter{ResponseWriter: &plainResponseWriter{header: make(http.Header)}}
	if _, ok := plain.wrap().(http.Flusher); ok {
		t.Fatal("unsupported Flush exposed")
	}
	if _, ok := plain.wrap().(http.Hijacker); ok {
		t.Fatal("unsupported Hijack exposed")
	}
	if _, ok := plain.wrap().(http.Pusher); ok {
		t.Fatal("unsupported Push exposed")
	}
	flushed := &loggedResponseWriter{ResponseWriter: &capabilityWriter{header: make(http.Header)}}
	if err := http.NewResponseController(flushed.wrap()).Flush(); err != nil || flushed.status != 200 {
		t.Fatalf("flush status=%d err=%v", flushed.status, err)
	}
	upgrade := &loggedResponseWriter{ResponseWriter: w}
	upgrade.WriteHeader(101)
	if upgrade.status != 101 {
		t.Fatalf("upgrade status=%d", upgrade.status)
	}
}

type plainResponseWriter struct{ header http.Header }

func (w *plainResponseWriter) Header() http.Header         { return w.header }
func (w *plainResponseWriter) WriteHeader(int)             {}
func (w *plainResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestServerRequestLoggingCoversHealthStaticAndMiddlewareErrors(t *testing.T) {
	output := captureRequestLogs(t)
	db, err := store.Open(context.Background(), config.Config{DBEngine: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New(db, config.Config{DBEngine: "sqlite"}, t.TempDir())
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health", 200},
		{"GET", "/static/missing.css", 404},
		{"POST", "/settings/update", 403},
		{"GET", "/api/bookmarks", 301},
	} {
		output.Reset()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code != tc.status || !strings.Contains(output.String(), "status="+strconv.Itoa(tc.status)) {
			t.Fatalf("%s %s status=%d log=%q", tc.method, tc.path, response.Code, output.String())
		}
	}
}
