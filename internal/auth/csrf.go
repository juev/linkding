package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
)

const csrfAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func NewCSRFSecret() (string, error) {
	var result [32]byte
	var random [32]byte
	for i := 0; i < len(result); {
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate CSRF secret: %w", err)
		}
		for _, value := range random {
			if value < 248 {
				result[i] = csrfAlphabet[int(value)%len(csrfAlphabet)]
				i++
				if i == len(result) {
					break
				}
			}
		}
	}
	return string(result[:]), nil
}

// MaskCSRF emits Django's 64-character form token from the cookie secret.
func MaskCSRF(secret string) (string, error) {
	if !validCSRFChars(secret) || len(secret) != 32 {
		return "", fmt.Errorf("invalid CSRF secret")
	}
	mask, err := NewCSRFSecret()
	if err != nil {
		return "", err
	}
	var cipher [32]byte
	for i := range cipher {
		cipher[i] = csrfAlphabet[(csrfIndex(secret[i])+csrfIndex(mask[i]))%len(csrfAlphabet)]
	}
	return mask + string(cipher[:]), nil
}

func VerifyCSRF(secret, submitted string) bool {
	if len(secret) != 32 || !validCSRFChars(secret) || !validCSRFChars(submitted) {
		return false
	}
	if len(submitted) == 32 {
		return subtle.ConstantTimeCompare([]byte(secret), []byte(submitted)) == 1
	}
	if len(submitted) != 64 {
		return false
	}
	var unmasked [32]byte
	for i := range unmasked {
		value := csrfIndex(submitted[i+32]) - csrfIndex(submitted[i])
		if value < 0 {
			value += len(csrfAlphabet)
		}
		unmasked[i] = csrfAlphabet[value]
	}
	return subtle.ConstantTimeCompare([]byte(secret), unmasked[:]) == 1
}

func validCSRFChars(value string) bool {
	for i := range value {
		if csrfIndex(value[i]) < 0 {
			return false
		}
	}
	return true
}

func csrfIndex(char byte) int {
	for i := range csrfAlphabet {
		if csrfAlphabet[i] == char {
			return i
		}
	}
	return -1
}
