package secrets

import (
	"errors"
	"testing"
)

func newTestKeyring(t *testing.T) *Keyring {
	t.Helper()

	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	k, err := NewKeyring(key)
	if err != nil {
		t.Fatalf("failed to build keyring: %v", err)
	}
	return k
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	k := newTestKeyring(t)

	values := []string{
		"hunter2",
		"a password with spaces and ünïcödé",
		"{\"host\":\"smtp.example.com\",\"password\":\"s3cret\"}",
	}

	for _, want := range values {
		t.Run(want, func(t *testing.T) {
			encrypted, err := k.Encrypt(want)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}
			if encrypted == want {
				t.Fatal("value was stored in the clear")
			}
			if !IsEncrypted(encrypted) {
				t.Error("expected the ciphertext to carry the format marker")
			}

			got, err := k.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}
			if got != want {
				t.Errorf("Decrypt() = %q; want %q", got, want)
			}
		})
	}
}

// The same input must not produce the same ciphertext, or equal passwords
// across providers would be visible as equal blobs in the database.
func TestEncryptionIsRandomised(t *testing.T) {
	k := newTestKeyring(t)

	first, err := k.Encrypt("same-value")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	second, err := k.Encrypt("same-value")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if first == second {
		t.Error("identical plaintexts produced identical ciphertexts")
	}
}

// Values written before encryption existed must keep working, and be readable
// so they can be upgraded on the next save.
func TestPlaintextPassesThrough(t *testing.T) {
	k := newTestKeyring(t)

	got, err := k.Decrypt("legacy-plaintext-password")
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != "legacy-plaintext-password" {
		t.Errorf("Decrypt() = %q; want the value unchanged", got)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	encrypted, err := newTestKeyring(t).Encrypt("hunter2")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if _, err := newTestKeyring(t).Decrypt(encrypted); err == nil {
		t.Error("expected decryption with a different key to fail")
	}
}

func TestTamperedCiphertextIsRejected(t *testing.T) {
	k := newTestKeyring(t)

	encrypted, err := k.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	tampered := encrypted[:len(encrypted)-1] + "A"
	if _, err := k.Decrypt(tampered); err == nil {
		t.Error("expected a tampered ciphertext to be rejected")
	}
}

func TestEmptyValuesStayEmpty(t *testing.T) {
	k := newTestKeyring(t)

	encrypted, err := k.Encrypt("")
	if err != nil || encrypted != "" {
		t.Errorf("Encrypt(\"\") = %q, %v; want \"\", nil", encrypted, err)
	}

	decrypted, err := k.Decrypt("")
	if err != nil || decrypted != "" {
		t.Errorf("Decrypt(\"\") = %q, %v; want \"\", nil", decrypted, err)
	}
}

func TestInvalidKeysAreRejected(t *testing.T) {
	tests := []string{"", "short", "zzzz", "abcd1234"}

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			if _, err := NewKeyring(key); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("NewKeyring(%q) error = %v; want ErrInvalidKey", key, err)
			}
		})
	}
}
