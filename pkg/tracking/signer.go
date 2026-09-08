// Package tracking mints and verifies the open/click links embedded in
// outgoing mail.
package tracking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
)

var (
	ErrMissingSignature = errors.New("tracking link is not signed")
	ErrBadSignature     = errors.New("tracking link signature does not match")
	ErrUnsupportedURL   = errors.New("tracking target must be an http or https URL")
	ErrNoKey            = errors.New("tracking signer has no key")
)

// SignatureParam is the query parameter carrying the signature.
const SignatureParam = "sig"

// Link describes what a tracking URL asserts.
type Link struct {
	Kind      string // "open" or "click"
	TenantID  string
	MessageID string
	Recipient string
	TargetURL string // click links only
}

// Signer signs and verifies tracking links.
//
// Without a signature anyone can record opens and clicks for any tenant and
// message, and a click link becomes an open redirect on the very domain
// recipients are taught to trust. The signature covers the target URL, so the
// redirect can only go where this server said it could.
// The key is swappable because a link has to verify under the key that signed
// it, and on a fresh install the key does not exist until the setup wizard
// generates one. A signer built before setup and never updated goes on signing
// with whatever it started with, while the next restart reads the real key from
// the config file and rejects every link already sitting in a recipient's inbox
// — permanently, because those messages are delivered and cannot be reissued.
//
// So setup swaps the key in here, exactly as it swaps the token maker, and every
// holder of this pointer follows it.
type Signer struct {
	mu  sync.RWMutex
	key []byte
}

func NewSigner(key []byte) *Signer {
	return &Signer{key: key}
}

// DeriveKey turns the instance's symmetric key into the tracking key.
//
// Both the startup path and the setup wizard call this, so there is one
// definition of the derivation rather than two that can drift apart.
func DeriveKey(symmetricKey string) []byte {
	sum := sha256.Sum256([]byte("panmail-tracking-v1:" + symmetricKey))
	return sum[:]
}

// SetKey installs the key used from now on.
func (s *Signer) SetKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = key
}

// HasKey reports whether this signer can produce a signature that will still
// verify after a restart. Callers must not mint links when it is false.
func (s *Signer) HasKey() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.key) > 0
}

func (s *Signer) mac(link Link) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.key) == 0 {
		return nil, ErrNoKey
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(canonical(link)))
	return mac.Sum(nil), nil
}

// Sign returns the signature for a link, or "" if this signer has no key.
func (s *Signer) Sign(link Link) string {
	sum, err := s.mac(link)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(sum)
}

// Verify checks a signature against a link.
func (s *Signer) Verify(link Link, signature string) error {
	if signature == "" {
		return ErrMissingSignature
	}

	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return ErrBadSignature
	}

	sum, err := s.mac(link)
	if err != nil {
		return err
	}
	if !hmac.Equal(provided, sum) {
		return ErrBadSignature
	}
	return nil
}

// canonical renders a link as an unambiguous string. Fields are separated by a
// byte that cannot appear in any of them, so no combination of values can be
// rearranged into another valid link.
func canonical(link Link) string {
	return strings.Join([]string{
		link.Kind,
		link.TenantID,
		link.MessageID,
		link.Recipient,
		link.TargetURL,
	}, "\x00")
}

// ValidateTarget rejects redirect targets that are not ordinary web links.
//
// Signing already prevents an outsider choosing the destination, so this is
// about what a tenant may put in their own templates: a "javascript:" or
// "data:" destination reached through a link on the gateway's domain is a
// phishing primitive regardless of who authored it.
func ValidateTarget(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ErrUnsupportedURL
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return ErrUnsupportedURL
	}

	if parsed.Host == "" {
		return ErrUnsupportedURL
	}
	return nil
}
