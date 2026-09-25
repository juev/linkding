package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSecretKeyCreatesDjangoCompatibleKey(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := ensureSecretKey(dataDir); err != nil {
		t.Fatalf("ensureSecretKey: %v", err)
	}

	key, err := os.ReadFile(filepath.Join(dataDir, "secretkey.txt"))
	if err != nil {
		t.Fatalf("read secret key: %v", err)
	}
	if len(key) != 50 {
		t.Fatalf("key length = %d, want 50", len(key))
	}
	for _, char := range key {
		if !strings.ContainsRune(djangoSecretKeyAlphabet, rune(char)) {
			t.Fatalf("key contains character outside Django alphabet: %q", char)
		}
	}
}

func TestEnsureSecretKeyPreservesExistingFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "secretkey.txt")
	const existing = "existing-secret-key"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("write existing key: %v", err)
	}

	if err := ensureSecretKey(dataDir); err != nil {
		t.Fatalf("ensureSecretKey: %v", err)
	}
	key, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read secret key: %v", err)
	}
	if string(key) != existing {
		t.Fatalf("key = %q, want existing value %q", key, existing)
	}
}
