package usecases

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	gsmail "github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

// Whether a message panmail signs actually verifies.
//
// The existing DKIM tests are about configuration — that a provider can have
// DKIM added, that the provider's domain wins over the request's. None of them
// look at a signed message, so none would notice the failure that matters: a
// key stored in a format the signer cannot parse, a selector that does not
// match, a canonicalisation that mangles the headers. Every one of those sends
// successfully and fails at the receiver, and the only symptom is that mail
// starts landing in spam.
//
// So: sign through the real provider factory, capture the bytes as they go out,
// and verify them with an independent implementation.

// captureSMTP is a listener that speaks enough SMTP to accept one message and
// hands back exactly what was written after DATA.
type captureSMTP struct {
	addr     string
	messages chan []byte
	ln       net.Listener
	wg       sync.WaitGroup
}

func newCaptureSMTP(t *testing.T) *captureSMTP {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &captureSMTP{addr: ln.Addr().String(), messages: make(chan []byte, 4), ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); s.wg.Wait() })
	return s
}

func (s *captureSMTP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	say := func(line string) { _, _ = w.WriteString(line + "\r\n"); _ = w.Flush() }

	say("220 capture ESMTP")
	var body bytes.Buffer
	inData := false

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")

		if inData {
			if trimmed == "." {
				inData = false
				out := make([]byte, body.Len())
				copy(out, body.Bytes())
				select {
				case s.messages <- out:
				default:
				}
				body.Reset()
				say("250 OK")
				continue
			}
			// Dot-stuffing, undone: a body line starting with "." arrives
			// doubled, and leaving it doubled would change the bytes the
			// signature covers.
			body.WriteString(strings.TrimPrefix(trimmed, "."))
			body.WriteString("\r\n")
			continue
		}

		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			// AUTH is advertised because the provider under test has
			// credentials, and gsmail declines to hand them to a server that
			// cannot accept them.
			say("250-capture")
			say("250-AUTH PLAIN LOGIN")
			say("250 8BITMIME")
		case strings.HasPrefix(upper, "AUTH"):
			if strings.Contains(upper, "LOGIN") && len(strings.Fields(trimmed)) == 2 {
				say("334 VXNlcm5hbWU6")
				_, _ = r.ReadString('\n')
				say("334 UGFzc3dvcmQ6")
				_, _ = r.ReadString('\n')
			}
			say("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
			say("250 OK")
		case upper == "DATA":
			inData = true
			say("354 End data with <CRLF>.<CRLF>")
		case upper == "QUIT":
			say("221 Bye")
			return
		default:
			say("250 OK")
		}
	}
}

// signingKey returns a key in the PEM form an operator pastes into the UI.
func newSigningKeyPEM(t *testing.T) (pemKey string, public *rsa.PublicKey) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block)), &key.PublicKey
}

// dnsRecord serves the public key the way a DNS TXT record would.
type dnsRecord struct{ value string }

func (d dnsRecord) LookupTXT(domain string) ([]string, error) { return []string{d.value}, nil }

func publicRecord(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
}

func TestAMessagePanmailSignsVerifies(t *testing.T) {
	const (
		domain   = "example.com"
		selector = "panmail"
	)

	server := newCaptureSMTP(t)
	privatePEM, pub := newSigningKeyPEM(t)

	host, portStr, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatal(err)
	}

	// Through the real factory, from the same protojson config a stored
	// provider holds — not a hand-built gsmail sender, because the mapping
	// from stored configuration to signing options is the part that can be
	// wrong.
	config, err := protojson.Marshal(&panmailv1.SmtpConfig{
		Host: host, Port: int32(port),
		Username: "user", Password: "pass",
		UseSsl: false, SkipVerify: true,
		Dkim: &panmailv1.DkimConfig{
			Domain:     domain,
			Selector:   selector,
			PrivateKey: privatePEM,
		},
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	sender, err := NewProviderFactory().CreateSender(&entities.EmailProvider{
		ID: "p1", TenantID: "t1", Name: "SMTP",
		Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP, Config: config,
	})
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = sender.Send(ctx, gsmail.Email{
		From:    "sender@" + domain,
		To:      []string{"recipient@example.org"},
		Subject: "signed",
		Body:    []byte("does this verify"),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	var raw []byte
	select {
	case raw = <-server.messages:
	case <-time.After(10 * time.Second):
		t.Fatal("the server never received a message")
	}

	if !bytes.Contains(raw, []byte("DKIM-Signature:")) {
		t.Fatalf("the message carries no DKIM-Signature header:\n%s", firstLines(raw, 15))
	}

	// The actual question. An independent implementation, checking the
	// signature against the public key a receiver would find in DNS.
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{
		LookupTXT: dnsRecord{value: publicRecord(t, pub)}.LookupTXT,
	})
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, firstLines(raw, 15))
	}
	if len(verifications) == 0 {
		t.Fatal("no signatures were verified")
	}
	for _, v := range verifications {
		if v.Err != nil {
			t.Errorf("the signature panmail produced does not verify: %v\n"+
				"every receiver would fail this message and most would treat it as spam", v.Err)
		}
		if v.Domain != domain {
			t.Errorf("signature domain = %q, want %q", v.Domain, domain)
		}
	}
}

// A signature is only worth having if it breaks when the message is altered.
// One that verifies against modified content is not protecting anything.
func TestTheSignatureFailsIfTheMessageIsAltered(t *testing.T) {
	const domain = "example.com"

	server := newCaptureSMTP(t)
	privatePEM, pub := newSigningKeyPEM(t)

	host, portStr, _ := net.SplitHostPort(server.addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	config, _ := protojson.Marshal(&panmailv1.SmtpConfig{
		Host: host, Port: int32(port), Username: "u", Password: "p", SkipVerify: true,
		Dkim: &panmailv1.DkimConfig{Domain: domain, Selector: "panmail", PrivateKey: privatePEM},
	})
	sender, err := NewProviderFactory().CreateSender(&entities.EmailProvider{
		ID: "p2", TenantID: "t1", Name: "SMTP",
		Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP, Config: config,
	})
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sender.Send(ctx, gsmail.Email{
		From: "sender@" + domain, To: []string{"r@example.org"},
		Subject: "signed", Body: []byte("original body"),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	var raw []byte
	select {
	case raw = <-server.messages:
	case <-time.After(10 * time.Second):
		t.Fatal("no message received")
	}

	// The Subject, because it is covered by the signature and is present
	// verbatim on the wire — the body may be transfer-encoded, so searching
	// for its plaintext finds nothing and would make this test vacuous.
	tampered := bytes.Replace(raw, []byte("Subject: signed"), []byte("Subject: altered"), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatalf("could not find the Subject header to tamper with:\n%s", firstLines(raw, 15))
	}

	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(tampered), &dkim.VerifyOptions{
		LookupTXT: dnsRecord{value: publicRecord(t, pub)}.LookupTXT,
	})
	if err != nil {
		return // a hard verification failure is also a rejection
	}
	for _, v := range verifications {
		if v.Err == nil {
			t.Error("a modified body still verified; the signature covers nothing useful")
		}
	}
}

func firstLines(b []byte, n int) string {
	lines := strings.SplitN(string(b), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
