// Package secrets encrypts credentials that Panmail must be able to read back,
// such as SMTP passwords and webhook signing keys.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// EnvKeyName is the environment variable holding the data encryption key, as
// 64 hex characters (32 bytes).
const EnvKeyName = "PANMAIL_SECRET_KEY"

// cipherPrefix marks a value this package produced, so plaintext written
// before encryption existed is still readable and can be upgraded in place.
const cipherPrefix = "enc:v1:"

var (
	ErrNoKey      = errors.New("no encryption key configured")
	ErrInvalidKey = errors.New("encryption key must be 64 hex characters (32 bytes)")
)

// Keyring encrypts and decrypts credential values. It is safe for concurrent
// use: the underlying AEAD is stateless.
type Keyring struct {
	aead cipher.AEAD
}

// NewKeyring builds a keyring from a hex-encoded 32-byte key.
func NewKeyring(keyHex string) (*Keyring, error) {
	key, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(key) != 32 {
		return nil, ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &Keyring{aead: aead}, nil
}

// GenerateKey returns a new hex-encoded key.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// Encrypt returns the ciphertext for a value. An empty value stays empty so
// that "unset" is distinguishable from "encrypted empty string".
func (k *Keyring) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if k == nil {
		return "", ErrNoKey
	}

	nonce := make([]byte, k.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := k.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return cipherPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. A value without the marker is returned unchanged:
// it predates encryption and will be re-encrypted the next time it is saved.
func (k *Keyring) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !IsEncrypted(value) {
		return value, nil
	}
	if k == nil {
		return "", ErrNoKey
	}

	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, cipherPrefix))
	if err != nil {
		return "", fmt.Errorf("stored secret is not valid base64: %w", err)
	}

	nonceSize := k.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("stored secret is too short to be valid")
	}

	plaintext, err := k.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("stored secret could not be decrypted with the configured key: %w", err)
	}
	return string(plaintext), nil
}

// IsEncrypted reports whether a stored value is in this package's format.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, cipherPrefix)
}
