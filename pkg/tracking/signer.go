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
)

var (
	ErrMissingSignature = errors.New("tracking link is not signed")
	ErrBadSignature     = errors.New("tracking link signature does not match")
	ErrUnsupportedURL   = errors.New("tracking target must be an http or https URL")
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
type Signer struct {
	key []byte
}

func NewSigner(key []byte) *Signer {
	return &Signer{key: key}
}

// Sign returns the signature for a link.
func (s *Signer) Sign(link Link) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(canonical(link)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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

	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(canonical(link)))
	if !hmac.Equal(provided, mac.Sum(nil)) {
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
