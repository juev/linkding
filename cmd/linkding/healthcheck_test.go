package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/juev/linkding/internal/config"
)

func TestHealthcheckUsesConfiguredPortAndContextPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/linkding/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ServerPort: port, ContextPath: "linkding/"}
	if err := checkHealth(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ContextPath = "wrong/"
	if err := checkHealth(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("wrong context path: %v", err)
	}
	server.Close()
	if err := checkHealth(context.Background(), cfg); err == nil {
		t.Fatal("stopped server passed healthcheck")
	}
}
