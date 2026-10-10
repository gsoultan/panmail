package http

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	inboundpostgres "github.com/gsoultan/panmail/internal/inbound/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/storetest"
)

const (
	sesTopic   = "arn:aws:sns:us-east-1:123456789012:inbound-mail"
	sesCertURL = "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-test.pem"
)

// fakeAWS answers the requests SNS handling makes: the signing certificate at
// sesCertURL, and anything else with whatever status is configured. It
// records every URL it was asked for, so a test can assert that nothing was
// fetched from where it should not have been.
type fakeAWS struct {
	mu       sync.Mutex
	certPEM  []byte
	status   int    // for anything that is not the certificate
	location string // a redirect target, when status is a 3xx
	certDown bool
	visited  []string
}

func (f *fakeAWS) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.visited = append(f.visited, r.URL.String())
	f.mu.Unlock()

	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}
	switch {
	case r.URL.String() == sesCertURL && f.certDown:
		resp.StatusCode = http.StatusServiceUnavailable
	case r.URL.String() == sesCertURL:
		resp.Body = io.NopCloser(bytes.NewReader(f.certPEM))
	case f.status != 0:
		resp.StatusCode = f.status
		if f.location != "" {
			resp.Header.Set("Location", f.location)
		}
	}
	return resp, nil
}

func (f *fakeAWS) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.visited...)
}

// snsSigner is a generated key and a self-signed certificate standing in for
// AWS's.
type snsSigner struct {
	key *rsa.PrivateKey
	aws *fakeAWS
}

func newSNSSigner(t *testing.T) *snsSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return &snsSigner{key: key, aws: &fakeAWS{certPEM: certPEM}}
}

// sign signs an SNS envelope the way AWS does: the present fields among the
// signable ones, each as "name\nvalue\n", hashed per SignatureVersion.
func (s *snsSigner) sign(t *testing.T, envelope map[string]any) []byte {
	t.Helper()
	if _, ok := envelope["SigningCertURL"]; !ok {
		envelope["SigningCertURL"] = sesCertURL
	}
	if _, ok := envelope["SignatureVersion"]; !ok {
		envelope["SignatureVersion"] = "1"
	}

	var canonical strings.Builder
	for _, k := range []string{"Message", "MessageId", "Subject", "SubscribeURL", "Timestamp", "Token", "TopicArn", "Type"} {
		if v, ok := envelope[k].(string); ok {
			canonical.WriteString(k + "\n" + v + "\n")
		}
	}
	var (
		hashed []byte
		alg    crypto.Hash
	)
	if envelope["SignatureVersion"] == "2" {
		sum := sha256.Sum256([]byte(canonical.String()))
		hashed, alg = sum[:], crypto.SHA256
	} else {
		sum := sha1.Sum([]byte(canonical.String()))
		hashed, alg = sum[:], crypto.SHA1
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, alg, hashed)
	if err != nil {
		t.Fatal(err)
	}
	envelope["Signature"] = base64.StdEncoding.EncodeToString(sig)

	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (s *snsSigner) handler(uc *stubUsecase, topics string) *WebhookHandler {
	h := NewWebhookHandler(uc, providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_SES, topics)))
	h.sns = &gsmail.SNSVerifier{Client: &http.Client{Transport: s.aws}}
	client := newSNSClient()
	client.Transport = s.aws
	h.snsHTTP = client
	return h
}

func postSNS(h *WebhookHandler, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), bytes.NewReader(body))
	// SNS posts JSON as text/plain; the content type must not matter.
	req.Header.Set("Content-Type", "text/plain; charset=UTF-8")
	req.Header.Set("x-amz-sns-message-type", "Notification")
	return serve(h, req)
}

const rawMIME = "From: Alice <alice@example.com>\r\n" +
	"To: inbox@tenant.example\r\n" +
	"Subject: Hello from SES\r\n" +
	"Message-ID: <abc@example.com>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"The body.\r\n"

// received builds an SES "Received" notification as a receipt rule publishes
// it, with the message inline (an SNS action) or without it (an S3 action).
func received(t *testing.T, content, encoding string, s3 bool) string {
	t.Helper()
	action := map[string]any{"type": "SNS", "topicArn": sesTopic, "encoding": encoding}
	if s3 {
		action = map[string]any{"type": "S3", "topicArn": sesTopic, "bucketName": "inbound-bucket", "objectKey": "mail/o1"}
	}
	n := map[string]any{
		"notificationType": "Received",
		"mail": map[string]any{
			"timestamp":   "2026-10-10T09:00:00.000Z",
			"source":      "alice@example.com",
			"messageId":   "ses-msg-1",
			"destination": []string{"inbox@tenant.example"},
			"headers": []map[string]string{
				{"name": "From", "value": "Alice <alice@example.com>"},
				{"name": "Message-ID", "value": "<abc@example.com>"},
			},
			"commonHeaders": map[string]any{
				"from":    []string{"Alice <alice@example.com>"},
				"to":      []string{"inbox@tenant.example"},
				"subject": "Hello from SES",
			},
		},
		"receipt": map[string]any{"action": action},
	}
	if content != "" {
		n["content"] = content
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func notification(message string) map[string]any {
	return map[string]any{
		"Type":      "Notification",
		"MessageId": "sns-1",
		"TopicArn":  sesTopic,
		"Subject":   "Amazon SES Email Receipt Notification",
		"Message":   message,
		"Timestamp": "2026-10-10T09:00:00.000Z",
	}
}

// --- SES through SNS ---

// SES providers used to be refused outright on /inbound/, because what they
// store is a topic ARN and not a secret, so a tenant receiving through SES
// lost its inbound mail on upgrade. A post is now authenticated by SNS's own
// signature, under both signature versions.
func TestAnSESMessageSignedBySNSIsStored(t *testing.T) {
	for _, version := range []string{"1", "2"} {
		t.Run("SignatureVersion "+version, func(t *testing.T) {
			signer := newSNSSigner(t)
			uc := &stubUsecase{}
			h := signer.handler(uc, sesTopic)

			env := notification(received(t, rawMIME, "UTF8", false))
			env["SignatureVersion"] = version
			rec := postSNS(h, signer.sign(t, env))

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status %d (%s), want 202", rec.Code, rec.Body.String())
			}
			if len(uc.received) != 1 {
				t.Fatalf("processed %d, want 1", len(uc.received))
			}
			got := uc.received[0]
			if got.TenantId != tenantA || got.From != "alice@example.com" ||
				got.Subject != "Hello from SES" || !strings.Contains(got.BodyText, "The body.") {
				t.Errorf("stored %+v", got)
			}
		})
	}
}

func TestABase64EncodedSESMessageIsDecoded(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	env := notification(received(t, base64.StdEncoding.EncodeToString([]byte(rawMIME)), "BASE64", false))
	if rec := postSNS(h, signer.sign(t, env)); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if len(uc.received) != 1 || !strings.Contains(uc.received[0].BodyText, "The body.") {
		t.Fatalf("stored %+v", uc.received)
	}
}

// Mail too large for the SNS action goes to S3, and the notification carries
// only its headers. panmail does not fetch from the bucket; it stores what
// arrived, saying where the rest is.
func TestAnS3DeliveredSESMessageIsStoredWithItsLocation(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	if rec := postSNS(h, signer.sign(t, notification(received(t, "", "", true)))); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if len(uc.received) != 1 {
		t.Fatal("not stored")
	}
	got := uc.received[0]
	if got.Subject != "Hello from SES" || got.Headers[headerSESBucket] != "inbound-bucket" ||
		got.Headers[headerSESObjectKey] != "mail/o1" {
		t.Errorf("stored %+v", got)
	}
}

// SNS retries a notification it did not see a 2xx for. The retry is keyed on
// SES's id for the received message, so it is stored once.
func TestARetriedSNSNotificationIsStoredOnce(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)
	h.WithReceipts(inboundpostgres.NewReceiptStore(storetest.NewConnection(t)))

	body := signer.sign(t, notification(received(t, rawMIME, "UTF8", false)))
	for range 2 {
		if rec := postSNS(h, body); rec.Code != http.StatusAccepted {
			t.Fatalf("status %d, want 202", rec.Code)
		}
	}
	if len(uc.received) != 1 {
		t.Fatalf("processed %d, want once", len(uc.received))
	}
}

// Anyone can create an SNS topic and publish a correctly signed message to a
// public URL. Only the topics the provider names may post mail into it, and a
// provider that names none accepts nothing.
func TestSNSFromATopicTheProviderDoesNotNameIsRefused(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}

	cases := map[string]*WebhookHandler{
		"another topic":       signer.handler(uc, "arn:aws:sns:us-east-1:123456789012:something-else"),
		"no topic configured": signer.handler(uc, ""),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			signer.aws.visited = nil
			if rec := postSNS(h, signer.sign(t, notification(received(t, rawMIME, "UTF8", false)))); rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", rec.Code)
			}
			if got := signer.aws.fetched(); len(got) != 0 {
				t.Errorf("fetched %v before refusing", got)
			}
		})
	}
	if len(uc.received) != 0 {
		t.Fatal("stored mail from a topic nobody configured")
	}
}

func TestATamperedSNSMessageIsRefused(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	body := signer.sign(t, notification(received(t, rawMIME, "UTF8", false)))
	body = bytes.Replace(body, []byte("The body."), []byte("Pay this."), 1)

	if rec := postSNS(h, body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if len(uc.received) != 0 {
		t.Fatal("stored a message whose signature does not match")
	}
}

// The signing certificate is fetched before the signature can be checked, so
// its URL is whatever the poster wrote. It must be SNS, over https, in the
// topic's own region -- or panmail becomes a way to make requests to
// anything the poster names, internal addresses included.
func TestTheSigningCertificateIsFetchedOnlyFromTheTopicsSNSEndpoint(t *testing.T) {
	for _, certURL := range []string{
		"https://evil.example/cert.pem",
		"https://169.254.169.254/latest/meta-data/cert.pem",
		"http://sns.us-east-1.amazonaws.com/cert.pem",
		"https://sns.us-east-1.amazonaws.com:8443/cert.pem",
		"https://user@sns.us-east-1.amazonaws.com/cert.pem",
		"https://sns.eu-west-1.amazonaws.com/cert.pem",
		"https://sns.us-east-1.amazonaws.com.evil.example/cert.pem",
	} {
		t.Run(certURL, func(t *testing.T) {
			signer := newSNSSigner(t)
			uc := &stubUsecase{}
			h := signer.handler(uc, sesTopic)

			env := notification(received(t, rawMIME, "UTF8", false))
			env["SigningCertURL"] = certURL
			if rec := postSNS(h, signer.sign(t, env)); rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", rec.Code)
			}
			if got := signer.aws.fetched(); len(got) != 0 {
				t.Errorf("fetched %v", got)
			}
		})
	}
}

// A certificate AWS could not serve says nothing about whether the message is
// genuine. SNS is asked to retry rather than told the post is unauthorised.
func TestAnUnavailableSigningCertificateAsksForARetry(t *testing.T) {
	signer := newSNSSigner(t)
	signer.aws.certDown = true
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	if rec := postSNS(h, signer.sign(t, notification(received(t, rawMIME, "UTF8", false)))); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

// The provider's ARN list is shared with its delivery-event webhook. A bounce
// notification sent here by mistake is not mail and is not stored as mail.
func TestAnSNSNotificationThatIsNotReceivedMailIsRefused(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	msg := `{"notificationType":"Bounce","mail":{"messageId":"m"},"bounce":{"bounceType":"Permanent"}}`
	if rec := postSNS(h, signer.sign(t, notification(msg))); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if len(uc.received) != 0 {
		t.Fatal("stored a bounce notification as received mail")
	}
}

// --- subscription confirmation ---

func subscription(subscribeURL string) map[string]any {
	return map[string]any{
		"Type":         "SubscriptionConfirmation",
		"MessageId":    "sub-1",
		"Token":        "tok",
		"TopicArn":     sesTopic,
		"Message":      "You have chosen to subscribe to the topic.",
		"SubscribeURL": subscribeURL,
		"Timestamp":    "2026-10-10T09:00:00.000Z",
	}
}

const subscribeURL = "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&TopicArn=" + sesTopic + "&Token=tok"

func TestASubscriptionFromTheConfiguredTopicIsConfirmed(t *testing.T) {
	signer := newSNSSigner(t)
	uc := &stubUsecase{}
	h := signer.handler(uc, sesTopic)

	if rec := postSNS(h, signer.sign(t, subscription(subscribeURL))); rec.Code != http.StatusOK {
		t.Fatalf("status %d (%s), want 200", rec.Code, rec.Body.String())
	}
	if got := signer.aws.fetched(); len(got) != 2 || got[1] != subscribeURL {
		t.Fatalf("fetched %v, want the certificate then the SubscribeURL", got)
	}
	if len(uc.received) != 0 {
		t.Fatal("a subscription confirmation was stored as mail")
	}
}

// Confirming is an outbound request made on the body's say. It happens only
// for a signed message from a topic the provider names, and only to that
// topic's SNS endpoint -- never to a URL the message merely carries.
func TestASubscriptionIsNotConfirmedOutsideTheConfiguredTopicsEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		topics   string
		url      string
		wantCode int
	}{
		{"topic not configured", "arn:aws:sns:us-east-1:123456789012:other", subscribeURL, http.StatusUnauthorized},
		{"no topic configured", "", subscribeURL, http.StatusUnauthorized},
		{"SubscribeURL off SNS", sesTopic, "https://evil.example/?Action=ConfirmSubscription", http.StatusBadRequest},
		{"SubscribeURL to an internal address", sesTopic, "https://10.0.0.1/confirm", http.StatusBadRequest},
		{"SubscribeURL in another region", sesTopic, "https://sns.eu-west-1.amazonaws.com/?Action=ConfirmSubscription", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signer := newSNSSigner(t)
			h := signer.handler(&stubUsecase{}, tc.topics)

			if rec := postSNS(h, signer.sign(t, subscription(tc.url))); rec.Code != tc.wantCode {
				t.Fatalf("status %d, want %d", rec.Code, tc.wantCode)
			}
			for _, u := range signer.aws.fetched() {
				if u != sesCertURL {
					t.Errorf("fetched %s", u)
				}
			}
		})
	}
}

// The SNS endpoint was checked; wherever it redirects to was not. A redirect
// is not followed.
func TestASubscriptionConfirmationDoesNotFollowARedirect(t *testing.T) {
	signer := newSNSSigner(t)
	signer.aws.status = http.StatusFound
	signer.aws.location = "http://169.254.169.254/latest/meta-data/"
	h := signer.handler(&stubUsecase{}, sesTopic)

	if rec := postSNS(h, signer.sign(t, subscription(subscribeURL))); rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	for _, u := range signer.aws.fetched() {
		if strings.Contains(u, "169.254") {
			t.Fatalf("followed the redirect to %s", u)
		}
	}
}

// --- SendGrid Inbound Parse ---

func sendGridProvider(secret string) *fakeProviders {
	return providers(providerWith(panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID, secret))
}

const sendGridKey = "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE"

// SendGrid Inbound Parse can only be given a URL, so it authenticates with
// HTTP Basic credentials kept beside the event key in the provider's webhook
// secret. The event key itself is public and is never a credential.
func TestSendGridInboundParseAuthenticatesWithItsBasicCredential(t *testing.T) {
	body := "from=a%40b.c&to=d%40e.f&subject=hi&text=x"
	cases := []struct {
		name    string
		secret  string
		prepare func(*http.Request)
		want    int
	}{
		{"credential beside the key", sendGridKey + " inbound=parse:s3cret",
			func(r *http.Request) { r.SetBasicAuth("parse", "s3cret") }, http.StatusAccepted},
		{"password-only credential", "inbound=s3cret",
			func(r *http.Request) { r.SetBasicAuth("", "s3cret") }, http.StatusAccepted},
		{"wrong password", sendGridKey + " inbound=parse:s3cret",
			func(r *http.Request) { r.SetBasicAuth("parse", "guess") }, http.StatusUnauthorized},
		{"no credential configured, key as password", sendGridKey,
			func(r *http.Request) { r.SetBasicAuth("", sendGridKey) }, http.StatusUnauthorized},
		{"the key as password when a credential is configured", sendGridKey + " inbound=parse:s3cret",
			func(r *http.Request) { r.SetBasicAuth("", sendGridKey) }, http.StatusUnauthorized},
		{"HMAC keyed with the credential", sendGridKey + " inbound=parse:s3cret",
			func(r *http.Request) { r.Header.Set(SignatureHeader, sign("parse:s3cret", []byte(body))) }, http.StatusUnauthorized},
		{"no Authorization at all", sendGridKey + " inbound=parse:s3cret",
			func(*http.Request) {}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := &stubUsecase{}
			h := NewWebhookHandler(uc, sendGridProvider(tc.secret))
			req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			tc.prepare(req)

			if rec := serve(h, req); rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
			if stored := len(uc.received) == 1; stored != (tc.want == http.StatusAccepted) {
				t.Fatalf("stored = %v, want %v", stored, tc.want == http.StatusAccepted)
			}
		})
	}
}
