package http

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"
)

var (
	ErrNoWebhookSecret  = errors.New("no verification secret is configured for this provider")
	ErrBadWebhookSig    = errors.New("webhook signature does not match")
	ErrStaleWebhook     = errors.New("webhook timestamp is outside the accepted window")
	ErrMalformedWebhook = errors.New("webhook signature headers are missing or malformed")
)

// webhookTolerance bounds how old a signed webhook may be, so a captured
// request cannot be replayed indefinitely.
const webhookTolerance = 5 * time.Minute

// SecretResolver supplies the verification material configured for a provider.
type SecretResolver interface {
	WebhookSecret(tenantID, providerID string) (string, error)
}

// verifySendGrid checks SendGrid's ECDSA event webhook signature.
//
// The signed payload is the timestamp concatenated with the raw body, so the
// body must be verified exactly as received, before any parsing.
func verifySendGrid(publicKeyBase64, signature, timestamp string, body []byte) error {
	if publicKeyBase64 == "" {
		return ErrNoWebhookSecret
	}
	if signature == "" || timestamp == "" {
		return ErrMalformedWebhook
	}
	if err := checkTimestamp(timestamp); err != nil {
		return err
	}

	key, err := parseECDSAPublicKey(publicKeyBase64)
	if err != nil {
		return err
	}

	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return ErrMalformedWebhook
	}

	digest := sha256.Sum256(append([]byte(timestamp), body...))

	var parsed struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(sig, &parsed); err != nil {
		return ErrMalformedWebhook
	}

	if !ecdsa.Verify(key, digest[:], parsed.R, parsed.S) {
		return ErrBadWebhookSig
	}
	return nil
}

// verifyMailgun checks Mailgun's HMAC-SHA256 signature over timestamp+token.
func verifyMailgun(signingKey, timestamp, token, signature string) error {
	if signingKey == "" {
		return ErrNoWebhookSecret
	}
	if timestamp == "" || token == "" || signature == "" {
		return ErrMalformedWebhook
	}
	if err := checkTimestamp(timestamp); err != nil {
		return err
	}

	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(timestamp + token))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrBadWebhookSig
	}
	return nil
}

// verifyGeneric checks an HMAC-SHA256 over the raw body, for providers without
// a scheme of their own. The signature may carry a "sha256=" prefix.
func verifyGeneric(secret, signature string, body []byte) error {
	if secret == "" {
		return ErrNoWebhookSecret
	}
	if signature == "" {
		return ErrMalformedWebhook
	}

	provided := signature
	if len(provided) > 7 && provided[:7] == "sha256=" {
		provided = provided[7:]
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(provided)) {
		return ErrBadWebhookSig
	}
	return nil
}

// checkTimestamp bounds how far a signed request's timestamp may be from now.
//
// The comparison is done in whole seconds against time.Now().Unix() rather than
// by building a time.Time. Feeding a nanosecond-scale value (which is what a
// caller sending the wrong unit produces) into time.Unix overflows its internal
// representation, and the resulting duration can land back inside the window —
// so an absurd timestamp would read as current.
func checkTimestamp(raw string) error {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return ErrMalformedWebhook
	}

	now := time.Now().Unix()
	tolerance := int64(webhookTolerance / time.Second)

	if seconds > now+tolerance || seconds < now-tolerance {
		return ErrStaleWebhook
	}
	return nil
}

func parseECDSAPublicKey(encoded string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Also accept a PEM block, which is how the key is often copied.
		if block, _ := pem.Decode([]byte(encoded)); block != nil {
			der = block.Bytes
		} else {
			return nil, fmt.Errorf("%w: public key is not valid base64 or PEM", ErrMalformedWebhook)
		}
	}

	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedWebhook, err)
	}

	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: public key is not ECDSA", ErrMalformedWebhook)
	}
	return key, nil
}
