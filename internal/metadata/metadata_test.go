package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/juev/linkding/internal/httpclient"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
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

func TestLoadDecodesUTF8WhenCharsetAppearsAfterFirstKilobyte(t *testing.T) {
	page := `<html><head>` + strings.Repeat("<!-- preconnect -->", 70) + `<meta charset="UTF-8"><title>Лучший VPN</title><meta name="description" content="Наш VPN работает в России!"></head><body></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "Лучший VPN" || result.Description != "Наш VPN работает в России!" {
		t.Fatalf("UTF-8 metadata: %+v", result)
	}
}

func TestLoadRetainsLegacyEncodingWithInvalidUTF8(t *testing.T) {
	page, err := charmap.Windows1251.NewEncoder().Bytes([]byte(`<html><head><meta charset="windows-1251"><title>Привет</title><meta name="description" content="Описание"></head></html>`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(page)
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "Привет" || result.Description != "Описание" {
		t.Fatalf("legacy metadata: %+v", result)
	}
}

func TestLoadDecodesLateDeclaredLegacyCharset(t *testing.T) {
	for _, declaration := range []string{
		`<meta charset="windows-1251">`,
		`<meta http-equiv="Content-Type" content="text/html; charset=windows-1251">`,
	} {
		t.Run(declaration, func(t *testing.T) {
			page := `<html><head>` + strings.Repeat("<!-- preconnect -->", 70) + declaration + `<title>Привет</title><meta name="description" content="Описание"></head></html>`
			encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(page))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=UTF-8")
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
			if result.Title != "Привет" || result.Description != "Описание" {
				t.Fatalf("late charset metadata: %+v", result)
			}
		})
	}
}

func TestLoadDecodesHTTPDeclaredLegacyCharset(t *testing.T) {
	page, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(`<html><head><title>日本語</title><meta name="description" content="説明"></head></html>`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=Shift_JIS")
		_, _ = w.Write(page)
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "日本語" || result.Description != "説明" {
		t.Fatalf("HTTP charset metadata: %+v", result)
	}
}

func TestLoadPrefersValidUTF8OverIncorrectHTTPCharset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		_, _ = w.Write([]byte(`<html><head><title>Привет</title></head></html>`))
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "Привет" {
		t.Fatalf("UTF-8 metadata with incorrect header: %+v", result)
	}
}

func TestLoadRespectsUTF16BOM(t *testing.T) {
	page, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(`<html><head><title>Привет</title></head></html>`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		_, _ = w.Write(page)
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "Привет" {
		t.Fatalf("UTF-16 BOM metadata: %+v", result)
	}
}

func TestLoadDecodesSevenBitDeclaredEncoding(t *testing.T) {
	page := `<html><head>` + strings.Repeat("<!-- preconnect -->", 70) + `<meta charset="ISO-2022-JP"><title>日本語</title><meta name="description" content="説明"></head></html>`
	encoded, err := japanese.ISO2022JP.NewEncoder().Bytes([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(encoded) {
		t.Fatal("fixture must also be valid UTF-8 bytes")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "日本語" || result.Description != "説明" {
		t.Fatalf("ISO-2022-JP metadata: %+v", result)
	}
}

func TestLoadDecodesUTF16FromHTTPWithoutBOM(t *testing.T) {
	page, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().Bytes([]byte(`<html><head><title>Привет</title></head></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(page) {
		t.Fatal("fixture must also be valid UTF-8 bytes")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-16LE")
		_, _ = w.Write(page)
	}))
	defer server.Close()
	result := Load(context.Background(), httpclient.New("127.0.0.1", time.Second), server.URL)
	if result.Title != "Привет" {
		t.Fatalf("UTF-16LE metadata: %+v", result)
	}
}
