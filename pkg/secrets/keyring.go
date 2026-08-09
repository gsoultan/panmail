// Package secrets encrypts credentials that Panmail must be able to read back,
// such as SMTP passwords, DKIM private keys and webhook signing keys.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
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

// EnvRetiredKeysName holds keys that are no longer used for encryption but are
// still needed to read values written before a rotation. Comma-separated, in
// the same hex form.
//
// Rotation without this is not possible: a key can only be replaced if the old
// one is still available to read what it wrote, and re-encrypting everything
// requires both keys at once. Before this existed, a suspected key compromise
// had no remedy short of re-entering every credential by hand.
const EnvRetiredKeysName = "PANMAIL_SECRET_KEYS_RETIRED"

const (
	// v1 carries no key identity, so a value in this format has to be tried
	// against every key. Still read, never written.
	cipherPrefixV1 = "enc:v1:"
	// v2 names the key that wrote it, which is what makes a rotation
	// verifiable: without it there is no way to tell which values have already
	// been moved to the new key and which are still on the old one.
	cipherPrefixV2 = "enc:v2:"
)

var (
	ErrNoKey      = errors.New("no encryption key configured")
	ErrInvalidKey = errors.New("encryption key must be 64 hex characters (32 bytes)")
)

type keyEntry struct {
	id   string
	aead cipher.AEAD
}

// Keyring encrypts with one key and decrypts with several. It is safe for
// concurrent use: the underlying AEADs are stateless and the set is fixed at
// construction.
type Keyring struct {
	primary keyEntry
	// Retired keys, decrypt-only, in the order supplied.
	retired []keyEntry
}

// keyID is a short, non-secret fingerprint used to tag ciphertext.
//
// A hash rather than an operator-chosen label, so two deployments cannot
// disagree about what "key 2" means, and so nothing about the key itself has
// to be written next to the data it protects.
func keyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

func newEntry(keyHex string) (keyEntry, error) {
	key, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(key) != 32 {
		return keyEntry{}, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return keyEntry{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keyEntry{}, err
	}
	return keyEntry{id: keyID(key), aead: aead}, nil
}

// NewKeyring builds a keyring that encrypts with primaryHex.
//
// Any retired keys are used for decryption only, so values written before a
// rotation stay readable while they are moved across.
func NewKeyring(primaryHex string, retiredHex ...string) (*Keyring, error) {
	primary, err := newEntry(primaryHex)
	if err != nil {
		return nil, err
	}

	k := &Keyring{primary: primary}
	for _, h := range retiredHex {
		if strings.TrimSpace(h) == "" {
			continue
		}
		entry, err := newEntry(h)
		if err != nil {
			return nil, fmt.Errorf("retired key: %w", err)
		}
		// A retired key that is also the primary is a configuration mistake
		// rather than a failure; ignoring it keeps the rotation idempotent.
		if entry.id == primary.id {
			continue
		}
		k.retired = append(k.retired, entry)
	}
	return k, nil
}

// ParseRetiredKeys splits the comma-separated form the environment uses.
func ParseRetiredKeys(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// PrimaryKeyID identifies the key new values are written with. Non-secret.
func (k *Keyring) PrimaryKeyID() string {
	if k == nil {
		return ""
	}
	return k.primary.id
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

	nonce := make([]byte, k.primary.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := k.primary.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return cipherPrefixV2 + k.primary.id + ":" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt, using whichever key wrote the value.
//
// A value without a marker is returned unchanged: it predates encryption and
// will be re-encrypted the next time it is saved.
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

	id, payload, err := split(value)
	if err != nil {
		return "", err
	}

	// A v2 value names its key, so exactly one can work and a failure is a
	// real failure rather than a key worth trying.
	if id != "" {
		entry, ok := k.entryFor(id)
		if !ok {
			return "", fmt.Errorf(
				"stored secret was written with key %s, which is not configured; "+
					"add it to %s to read it", id, EnvRetiredKeysName)
		}
		return open(entry, payload)
	}

	// v1 carries no identity, so every key is a candidate. Primary first: after
	// a rotation most values are already on it.
	for _, entry := range append([]keyEntry{k.primary}, k.retired...) {
		if plaintext, err := open(entry, payload); err == nil {
			return plaintext, nil
		}
	}
	return "", errors.New("stored secret could not be decrypted with any configured key")
}

// split separates the key id, if any, from the base64 payload.
func split(value string) (id string, payload []byte, err error) {
	var encoded string

	switch {
	case strings.HasPrefix(value, cipherPrefixV2):
		rest := strings.TrimPrefix(value, cipherPrefixV2)
		idPart, encPart, found := strings.Cut(rest, ":")
		if !found {
			return "", nil, errors.New("stored secret is missing its key identifier")
		}
		id, encoded = idPart, encPart
	case strings.HasPrefix(value, cipherPrefixV1):
		encoded = strings.TrimPrefix(value, cipherPrefixV1)
	default:
		return "", nil, errors.New("stored secret is not in a known format")
	}

	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("stored secret is not valid base64: %w", err)
	}
	return id, raw, nil
}

func open(entry keyEntry, raw []byte) (string, error) {
	nonceSize := entry.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("stored secret is too short to be valid")
	}
	plaintext, err := entry.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("stored secret could not be decrypted with key %s: %w", entry.id, err)
	}
	return string(plaintext), nil
}

func (k *Keyring) entryFor(id string) (keyEntry, bool) {
	if k.primary.id == id {
		return k.primary, true
	}
	for _, entry := range k.retired {
		if entry.id == id {
			return entry, true
		}
	}
	return keyEntry{}, false
}

// IsEncrypted reports whether a stored value is in this package's format.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, cipherPrefixV1) || strings.HasPrefix(value, cipherPrefixV2)
}

// NeedsRotation reports whether a value should be rewritten under the primary
// key — either because it predates key identity, or because it names a
// different key.
//
// This is what makes a rotation finishable: without it there is no way to know
// when every value has moved and the old key can safely be dropped.
func (k *Keyring) NeedsRotation(value string) bool {
	if k == nil || value == "" || !IsEncrypted(value) {
		return false
	}
	id, _, err := split(value)
	if err != nil {
		return false
	}
	return id != k.primary.id
}
