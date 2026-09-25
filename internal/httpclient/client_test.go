package httpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientBlocksPrivateAddressAndChecksRedirect(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }))
	defer private.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, private.URL, http.StatusFound) }))
	defer redirect.Close()
	blocked := New("", time.Second)
	if _, err := blocked.Get(private.URL); err == nil {
		t.Fatal("connected to loopback without allowlist")
	}
	redirectHost := strings.Replace(redirect.URL, "127.0.0.1", "localhost", 1)
	if _, err := New("localhost", time.Second).Get(redirectHost); err == nil {
		t.Fatal("followed redirect from allowed hostname to blocked IP")
	}
	allowed := New("127.0.0.1", time.Second)
	response, err := allowed.Get(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "secret" {
		t.Fatalf("allowed redirect: %q, %v", body, err)
	}
}

func TestClientBlocksMappedAndReservedAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "224.0.0.1", "::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2001:db8::1"} {
		if publicAddress(addr) {
			t.Errorf("classified %s as public", addr)
		}
	}
	for _, addr := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(addr) {
			t.Errorf("classified %s as private", addr)
		}
	}
}

func TestAllowlistSupportsHostAndNetworks(t *testing.T) {
	a := parseAllowlist(" .example.com, 192.168.0.0/16, [::1], invalid/range ")
	if !a.matchesHost("example.com") || !a.matchesHost("sub.example.com") || a.matchesHost("notexample.com") {
		t.Fatalf("hostname allowlist: %+v", a)
	}
	if !a.matchesAddress("192.168.1.2") || !a.matchesAddress("::1") || a.matchesAddress("10.0.0.1") {
		t.Fatalf("network allowlist: %+v", a)
	}
	if !parseAllowlist("*").allowAll {
		t.Fatal("wildcard not accepted")
	}
}

func TestClientHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New("*", time.Second).Do(req)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("canceled request: %v", err)
	}
}
