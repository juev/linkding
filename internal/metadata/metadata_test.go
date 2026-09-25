package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/juev/linkding/internal/httpclient"
)

func TestLoadExtractsPinnedHeadFieldsAndResolvesImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title> Page title </title><meta name="description" content=" First &amp; second "><meta property="og:description" content="Fallback"><meta property="og:image" content="/cover.png"></head><body>Ignored</body></html>`))
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL+"/article")
	if result.URL != server.URL+"/article" || result.Title != "Page title" || result.Description != "First & second" || result.PreviewImage != server.URL+"/cover.png" {
		t.Fatalf("metadata: %+v", result)
	}
}

func TestLoadFallsBackToOpenGraphAndSwallowsBlockedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<head><meta property="og:description" content="OpenGraph"></head>`))
	}))
	defer server.Close()
	allowed := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if allowed.Description != "OpenGraph" {
		t.Fatalf("fallback: %+v", allowed)
	}
	blocked := Load(context.Background(), httpclient.New("", time.Second), server.URL)
	if blocked.URL != server.URL || blocked.Title != "" || blocked.Description != "" || blocked.PreviewImage != "" {
		t.Fatalf("blocked metadata: %+v", blocked)
	}
}
