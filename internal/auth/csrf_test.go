package auth

import "testing"

func TestDjangoMaskedCSRFToken(t *testing.T) {
	// Captured from Django 6.0.7 in the pinned v1.47.0 image.
	secret := "abcdefghijklmnopqrstuvwxyzABCDEF"
	token := "Oa3mMGUtsbfXhK4wdTCDg0MQjUf2mogUOb5pQL0AAkp8tXiLtaUWAl8dHjFtORKp"
	if !VerifyCSRF(secret, token) || !VerifyCSRF(secret, secret) {
		t.Fatal("Django CSRF token was rejected")
	}
	masked, err := MaskCSRF(secret)
	if err != nil || len(masked) != 64 || !VerifyCSRF(secret, masked) {
		t.Fatalf("generated CSRF token: token=%q err=%v", masked, err)
	}
	if VerifyCSRF(secret, token[:63]) || VerifyCSRF(secret, "!"+token[1:]) || VerifyCSRF(secret, "another-secret") {
		t.Fatal("invalid CSRF token was accepted")
	}
}
