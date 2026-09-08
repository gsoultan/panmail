package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	authentities "github.com/gsoultan/panmail/internal/auth/entities"
	emailsmtp "github.com/gsoultan/panmail/internal/email/transports/smtp"
)

// The SMTP submission listener carries an API key as the SMTP password, which
// is a tenant's whole sending authority. These cover the flag combinations
// that decide whether it travels encrypted, and the one that decides whether
// the listener exists at all.

type stubKeyVerifier struct{}

func (stubKeyVerifier) VerifyApiKey(context.Context, string) (*authentities.ApiKey, error) {
	return nil, errors.New("not used")
}

type stubEmailSender struct{}

func (stubEmailSender) SendEmail(
	context.Context, string, *panmailv1.SendEmailRequest,
) (*panmailv1.SendEmailResponse, error) {
	return nil, errors.New("not used")
}

func TestSMTPListenerIsOffUnlessAskedFor(t *testing.T) {
	server, err := buildSMTPServer("", "", "", false, stubKeyVerifier{}, stubEmailSender{})
	if err != nil {
		t.Fatalf("buildSMTPServer() error = %v", err)
	}
	if server != nil {
		t.Fatal("an SMTP listener was built without --smtp-addr")
	}
}

func TestSMTPListenerRefusesToPutKeysOnTheWireInTheClear(t *testing.T) {
	testCases := []struct {
		name          string
		addr          string
		cert          string
		key           string
		allowInsecure bool
		wantErr       bool
	}{
		{
			name:    "no TLS and no explicit consent is refused",
			addr:    "0.0.0.0:587",
			wantErr: true,
		},
		{
			name:          "insecure auth is allowed when asked for explicitly",
			addr:          "127.0.0.1:587",
			allowInsecure: true,
		},
		{
			name:    "a certificate without a key is refused",
			addr:    "0.0.0.0:587",
			cert:    "cert.pem",
			wantErr: true,
		},
		{
			name:    "a key without a certificate is refused",
			addr:    "0.0.0.0:587",
			key:     "key.pem",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := buildSMTPServer(
				tc.addr, tc.cert, tc.key, tc.allowInsecure,
				stubKeyVerifier{}, stubEmailSender{},
			)

			if tc.wantErr {
				if err == nil {
					t.Fatal("buildSMTPServer() succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildSMTPServer() error = %v", err)
			}
			if server == nil {
				t.Fatal("buildSMTPServer() returned no server and no error")
			}
		})
	}
}

// A missing keypair must fail at startup, not at the first connection.
func TestSMTPListenerFailsFastOnAnUnreadableKeypair(t *testing.T) {
	_, err := buildSMTPServer(
		"0.0.0.0:587", "does-not-exist.pem", "does-not-exist.key", false,
		stubKeyVerifier{}, stubEmailSender{},
	)
	if err == nil {
		t.Fatal("buildSMTPServer() succeeded with an unreadable keypair")
	}
	if !strings.Contains(err.Error(), "SMTP TLS keypair") {
		t.Errorf("error = %v, want it to name the keypair", err)
	}
}

// The transport's own guard is what the wiring above relies on.
func TestInsecureAuthGuardIsTheTransportsOwn(t *testing.T) {
	_, err := emailsmtp.NewServer(
		emailsmtp.Config{Addr: "0.0.0.0:587"},
		stubKeyVerifier{}, stubEmailSender{}, nil,
	)
	if !errors.Is(err, emailsmtp.ErrInsecureAuthWithoutTLS) {
		t.Fatalf("error = %v, want ErrInsecureAuthWithoutTLS", err)
	}
}

// What the dashboard is told about the listener has to match what an
// integrator would actually have to dial. A wildcard bind names no such host,
// and reporting one would send them somewhere that does not answer.
func TestDescribedSMTPFlagsMatchTheFlags(t *testing.T) {
	// describeSMTPFlags is only reached when --smtp-addr was given, so there is
	// no disabled case here any more: "no address" is now the branch that hands
	// the listener to the supervisor instead, and TestSMTPListenerIsOffUnlessAskedFor
	// is what still covers it.
	testCases := []struct {
		name          string
		addr          string
		cert          string
		key           string
		allowInsecure bool
		wantHost      string
		wantPort      int
		wantStarttls  bool
		wantInsecure  bool
	}{
		{
			name:         "a named host is reported",
			addr:         "mail.example.com:587",
			cert:         "cert.pem",
			key:          "key.pem",
			wantHost:     "mail.example.com",
			wantPort:     587,
			wantStarttls: true,
		},
		{
			name:          "an ipv4 wildcard reports no host",
			addr:          "0.0.0.0:587",
			allowInsecure: true,
			wantHost:      "",
			wantPort:      587,
			wantInsecure:  true,
		},
		{
			name:          "a bare port reports no host",
			addr:          ":2525",
			allowInsecure: true,
			wantHost:      "",
			wantPort:      2525,
			wantInsecure:  true,
		},
		{
			name:          "an ipv6 wildcard reports no host",
			addr:          "[::]:587",
			allowInsecure: true,
			wantHost:      "",
			wantPort:      587,
			wantInsecure:  true,
		},
		{
			name:          "loopback is a real host and is reported",
			addr:          "127.0.0.1:587",
			allowInsecure: true,
			wantHost:      "127.0.0.1",
			wantPort:      587,
			wantInsecure:  true,
		},
		{
			name:          "TLS wins over the insecure flag",
			addr:          "mail.example.com:587",
			cert:          "cert.pem",
			key:           "key.pem",
			allowInsecure: true,
			wantHost:      "mail.example.com",
			wantPort:      587,
			wantStarttls:  true,
			wantInsecure:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeSMTPFlags(tc.addr, tc.cert, tc.key, tc.allowInsecure)

			if got.Host != tc.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tc.wantHost)
			}
			if got.Port != tc.wantPort {
				t.Errorf("Port = %d, want %d", got.Port, tc.wantPort)
			}
			if got.STARTTLS != tc.wantStarttls {
				t.Errorf("STARTTLS = %v, want %v", got.STARTTLS, tc.wantStarttls)
			}
			if got.InsecureAuthAllowed != tc.wantInsecure {
				t.Errorf("InsecureAuthAllowed = %v, want %v",
					got.InsecureAuthAllowed, tc.wantInsecure)
			}
		})
	}
}
