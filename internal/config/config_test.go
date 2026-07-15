package config

import (
	"encoding/hex"
	"testing"
)

func TestEncryption(t *testing.T) {
	key := hex.EncodeToString(make([]byte, 32)) // dummy key
	password := "my-secret-password"

	encrypted, err := encrypt(password, key)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	if encrypted == password {
		t.Errorf("encrypted password should be different from plain text")
	}

	decrypted, err := decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if decrypted != password {
		t.Errorf("decrypted password mismatch: got %s, want %s", decrypted, password)
	}
}
