package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// --- SendGrid ---------------------------------------------------------------

// sendGridKeypair returns a signer and the base64 PKIX public key a tenant
// would paste into the provider configuration.
func sendGridKeypair(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	return key, base64.StdEncoding.EncodeToString(der)
}

func signSendGrid(t *testing.T, key *ecdsa.PrivateKey, timestamp string, body []byte) string {
	t.Helper()

	digest := sha256.Sum256(append([]byte(timestamp), body...))
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func nowStamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

func TestVerifySendGridAcceptsAGenuineSignature(t *testing.T) {
	key, publicKey := sendGridKeypair(t)
	body := []byte(`[{"email":"a@example.com","event":"delivered"}]`)
	ts := nowStamp()

	if err := verifySendGrid(publicKey, signSendGrid(t, key, ts, body), ts, body); err != nil {
		t.Errorf("expected a genuine signature to verify, got %v", err)
	}
}

// Anything short of a genuine signature must be refused. A verifier that
// accepts by mistake looks identical to a working one until events are forged.
func TestVerifySendGridRefusesEverythingElse(t *testing.T) {
	key, publicKey := sendGridKeypair(t)
	_, otherPublicKey := sendGridKeypair(t)

	body := []byte(`[{"email":"a@example.com","event":"delivered"}]`)
	ts := nowStamp()
	good := signSendGrid(t, key, ts, body)

	stale := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)

	tests := []struct {
		name      string
		publicKey string
		signature string
		timestamp string
		body      []byte
		wantErr   error
	}{
		{"no configured secret", "", good, ts, body, ErrNoWebhookSecret},
		{"missing signature", publicKey, "", ts, body, ErrMalformedWebhook},
		{"missing timestamp", publicKey, good, "", body, ErrMalformedWebhook},
		{"non-numeric timestamp", publicKey, good, "not-a-time", body, ErrMalformedWebhook},
		{"signature is not base64", publicKey, "!!!not-base64!!!", ts, body, ErrMalformedWebhook},
		{"stale timestamp", publicKey, signSendGrid(t, key, stale, body), stale, body, ErrStaleWebhook},
		{"tampered body", publicKey, good, ts, []byte(`[{"email":"attacker@example.com","event":"delivered"}]`), ErrBadWebhookSig},
		{"signed by another key", otherPublicKey, good, ts, body, ErrBadWebhookSig},
		{"timestamp swapped after signing", publicKey, good, nowStamp() + "0", body, ErrStaleWebhook},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := verifySendGrid(tc.publicKey, tc.signature, tc.timestamp, tc.body)
			if err == nil {
				t.Fatal("verification succeeded when it must not")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifySendGridRejectsNonECDSAKeys(t *testing.T) {
	if err := verifySendGrid("bm90LWEta2V5", "c2ln", nowStamp(), []byte("{}")); err == nil {
		t.Error("expected a malformed public key to be rejected")
	}
}

// --- Mailgun ----------------------------------------------------------------

func signMailgun(signingKey, timestamp, token string) string {
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(timestamp + token))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyMailgun(t *testing.T) {
	const signingKey = "key-3ax6xnjp29jd6fds4gc373sgvjxteol0"
	const token = "a-random-token"

	ts := nowStamp()
	stale := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)

	tests := []struct {
		name      string
		key       string
		timestamp string
		token     string
		signature string
		wantErr   error
	}{
		{"genuine signature", signingKey, ts, token, signMailgun(signingKey, ts, token), nil},
		{"no configured secret", "", ts, token, signMailgun(signingKey, ts, token), ErrNoWebhookSecret},
		{"missing signature", signingKey, ts, token, "", ErrMalformedWebhook},
		{"missing token", signingKey, ts, "", signMailgun(signingKey, ts, token), ErrMalformedWebhook},
		{"stale timestamp", signingKey, stale, token, signMailgun(signingKey, stale, token), ErrStaleWebhook},
		{"wrong signature", signingKey, ts, token, signMailgun("another-key", ts, token), ErrBadWebhookSig},
		{"token swapped after signing", signingKey, ts, "different-token", signMailgun(signingKey, ts, token), ErrBadWebhookSig},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyMailgun(tc.key, tc.timestamp, tc.token, tc.signature)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

// --- Generic HMAC -----------------------------------------------------------

func signGeneric(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGeneric(t *testing.T) {
	const secret = "shared-webhook-secret"
	body := []byte(`{"event":"delivered","recipient":"a@example.com"}`)
	good := signGeneric(secret, body)

	tests := []struct {
		name      string
		secret    string
		signature string
		body      []byte
		wantErr   error
	}{
		{"genuine signature", secret, good, body, nil},
		{"sha256 prefix is accepted", secret, "sha256=" + good, body, nil},
		{"no configured secret", "", good, body, ErrNoWebhookSecret},
		{"missing signature", secret, "", body, ErrMalformedWebhook},
		{"tampered body", secret, good, []byte(`{"event":"bounced","recipient":"a@example.com"}`), ErrBadWebhookSig},
		{"wrong secret", secret, signGeneric("another-secret", body), body, ErrBadWebhookSig},
		{"empty signature body mismatch", secret, signGeneric(secret, []byte("")), body, ErrBadWebhookSig},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyGeneric(tc.secret, tc.signature, tc.body)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v; want %v", err, tc.wantErr)
			}
		})
	}
}

// --- Timestamp window -------------------------------------------------------

func TestCheckTimestampWindow(t *testing.T) {
	tests := []struct {
		name    string
		offset  time.Duration
		wantErr error
	}{
		{"now", 0, nil},
		{"just inside the window", -(webhookTolerance - time.Minute), nil},
		{"just outside the window", -(webhookTolerance + time.Minute), ErrStaleWebhook},
		{"far future", time.Hour, ErrStaleWebhook},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stamp := strconv.FormatInt(time.Now().Add(tc.offset).Unix(), 10)
			if err := checkTimestamp(stamp); !errors.Is(err, tc.wantErr) {
				t.Errorf("checkTimestamp(%s) = %v; want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestCheckTimestampRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "abc", "12.5", fmt.Sprint(time.Now().UnixNano())} {
		t.Run(raw, func(t *testing.T) {
			if err := checkTimestamp(raw); err == nil {
				t.Errorf("checkTimestamp(%q) accepted a value it should not", raw)
			}
		})
	}
}
