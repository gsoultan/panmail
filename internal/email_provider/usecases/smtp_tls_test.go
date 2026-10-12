package usecases

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type acceptAll struct{ delivered chan struct{} }

func (b acceptAll) NewSession(*gosmtp.Conn) (gosmtp.Session, error) { return b, nil }
func (acceptAll) Mail(string, *gosmtp.MailOptions) error            { return nil }
func (acceptAll) Rcpt(string, *gosmtp.RcptOptions) error            { return nil }
func (acceptAll) Reset()                                            {}
func (acceptAll) Logout() error                                     { return nil }
func (b acceptAll) Data(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	b.delivered <- struct{}{}
	return err
}

func rsaCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "legacy.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// A server whose STARTTLS offers only RSA key exchange — the shape of many
// older corporate mail servers — must still receive the message. With Go's
// default suite list the handshake fails with "remote error: tls: handshake
// failure" and nothing is delivered.
func TestSMTPDeliversToAServerWithOnlyRSAKeyExchange(t *testing.T) {
	backend := acceptAll{delivered: make(chan struct{}, 1)}
	srv := gosmtp.NewServer(backend)
	srv.Domain = "legacy.test"
	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{rsaCertificate(t)},
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	sender, err := buildSender(providerWith(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP, &panmailv1.SmtpConfig{
		Host:       host,
		Port:       int32(port),
		SkipVerify: true,
	}))
	if err != nil {
		t.Fatalf("build sender: %v", err)
	}
	t.Cleanup(func() { closeSender(sender) })
	sender.SetRetryConfig(gsmail.RetryConfig{MaxRetries: 0})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = sender.Send(ctx, gsmail.Email{
		From:    "sender@example.com",
		To:      []string{"someone@legacy.test"},
		Subject: "hello",
		Body:    []byte("hello"),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case <-backend.delivered:
	default:
		t.Fatal("server accepted the send but received no message")
	}
}
