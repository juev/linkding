package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

const djangoSecretKeyAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*(-_=+)"

func ensureSecretKey(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, "secretkey.txt")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("create secret key file: %w", err)
	}

	key := make([]byte, 50)
	for i := range key {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(djangoSecretKeyAlphabet))))
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return fmt.Errorf("generate secret key: %w", err)
		}
		key[i] = djangoSecretKeyAlphabet[index.Int64()]
	}
	if _, err := file.Write(key); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write secret key file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close secret key file: %w", err)
	}
	return nil
}
