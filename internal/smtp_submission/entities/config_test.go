package entities

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

// testKeypair returns a self-signed certificate and its key, both PEM.
func testKeypair(t *testing.T, notAfter time.Time) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtp.example.test"},
		DNSNames:     []string{"smtp.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func TestValidateGuardsWhatReachesTheNetwork(t *testing.T) {
	certPEM, keyPEM := testKeypair(t, time.Now().Add(24*time.Hour))
	otherCert, _ := testKeypair(t, time.Now().Add(24*time.Hour))

	testCases := []struct {
		name   string
		config Config

		wantErr error
		// wantAnyErr is for failures whose text comes from crypto/tls, where
		// asserting on a sentinel would be asserting on another package's
		// wording.
		wantAnyErr bool
	}{
		{
			name:   "the default is off and valid",
			config: *Default(),
		},
		{
			// The rule this package exists for. A dashboard session must not be
			// able to arrange for tenant API keys to cross a network in the
			// clear, which is what this combination does.
			name: "insecure auth on every interface is refused",
			config: Config{
				Enabled: true, BindScope: BindScopeAllInterfaces, Port: 587,
				AllowInsecureAuth: true,
			},
			wantErr: ErrInsecureAuthOffLoopback,
		},
		{
			// Refused even when the config is off: storing it would turn the
			// next toggle into a failure with no obvious cause.
			name: "insecure auth off loopback is refused even while disabled",
			config: Config{
				Enabled: false, BindScope: BindScopeAllInterfaces, Port: 587,
				AllowInsecureAuth: true,
			},
			wantErr: ErrInsecureAuthOffLoopback,
		},
		{
			name: "insecure auth on loopback is allowed",
			config: Config{
				Enabled: true, BindScope: BindScopeLoopback, Port: 587,
				AllowInsecureAuth: true,
			},
		},
		{
			name: "a public listener without a certificate is refused",
			config: Config{
				Enabled: true, BindScope: BindScopeAllInterfaces, Port: 587,
			},
			wantErr: ErrTLSRequired,
		},
		{
			name: "a public listener with a certificate is allowed",
			config: Config{
				Enabled: true, BindScope: BindScopeAllInterfaces, Port: 587,
				TLSCertPEM: certPEM, TLSKeyPEM: keyPEM,
			},
		},
		{
			// go-smtp would accept the connection and refuse every AUTH, which
			// is a listener that cannot do the one thing it is for.
			name: "enabling with no certificate and no insecure auth is refused",
			config: Config{
				Enabled: true, BindScope: BindScopeLoopback, Port: 587,
			},
			wantErr: ErrNoAuthPath,
		},
		{
			name: "port 25 is not a submission port",
			config: Config{
				Enabled: true, BindScope: BindScopeLoopback, Port: 25,
				AllowInsecureAuth: true,
			},
			wantErr: ErrRelayPort,
		},
		{
			name: "a port out of range is refused",
			config: Config{
				Enabled: true, BindScope: BindScopeLoopback, Port: 70000,
				AllowInsecureAuth: true,
			},
			wantErr: ErrInvalidPort,
		},
		{
			name: "an unknown bind scope is refused",
			config: Config{
				Enabled: true, BindScope: BindScope("everywhere"), Port: 587,
				AllowInsecureAuth: true,
			},
			wantErr: ErrInvalidBindScope,
		},
		{
			name: "a certificate without a key is refused",
			config: Config{
				BindScope: BindScopeLoopback, Port: 587, TLSCertPEM: certPEM,
			},
			wantErr: ErrIncompleteTLS,
		},
		{
			// The check that proves the key belongs to the certificate.
			// Verifying them separately would accept this and fail at the
			// first handshake instead of where somebody could fix it.
			name: "a key that does not match the certificate is refused",
			config: Config{
				Enabled: true, BindScope: BindScopeAllInterfaces, Port: 587,
				TLSCertPEM: otherCert, TLSKeyPEM: keyPEM,
			},
			wantAnyErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()

			if tc.wantAnyErr {
				if err == nil {
					t.Fatal("Validate() = nil, want an error")
				}
				return
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAddrFollowsTheBindScope(t *testing.T) {
	testCases := []struct {
		name  string
		scope BindScope
		want  string
	}{
		{name: "loopback is reachable only from this host", scope: BindScopeLoopback, want: "127.0.0.1:587"},
		{name: "all interfaces is a wildcard", scope: BindScopeAllInterfaces, want: "0.0.0.0:587"},
		{name: "an unset scope falls back to loopback", scope: "", want: "127.0.0.1:587"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{BindScope: tc.scope, Port: SubmissionPort}
			if got := cfg.Addr(); got != tc.want {
				t.Errorf("Addr() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDescribeReportsTheCertificateWithoutTheKey(t *testing.T) {
	certPEM, keyPEM := testKeypair(t, time.Now().Add(24*time.Hour))
	cfg := Config{TLSCertPEM: certPEM, TLSKeyPEM: keyPEM}

	info := cfg.Describe(time.Now())
	if info == nil {
		t.Fatal("Describe() = nil, want a description")
	}
	if len(info.DNSNames) != 1 || info.DNSNames[0] != "smtp.example.test" {
		t.Errorf("DNSNames = %v, want [smtp.example.test]", info.DNSNames)
	}
	if len(info.FingerprintSHA256) != 64 {
		t.Errorf("FingerprintSHA256 = %q, want 64 hex characters", info.FingerprintSHA256)
	}
	if info.Expired {
		t.Error("a certificate valid for another day was reported expired")
	}
}

func TestDescribeReportsExpiry(t *testing.T) {
	// Expiry is reported rather than made a validation failure: the listener
	// keeps serving the certificate it has, and saying so is more useful than
	// refusing to start over it.
	certPEM, keyPEM := testKeypair(t, time.Now().Add(time.Minute))
	cfg := Config{
		Enabled: true, BindScope: BindScopeAllInterfaces, Port: SubmissionPort,
		TLSCertPEM: certPEM, TLSKeyPEM: keyPEM,
	}

	info := cfg.Describe(time.Now().Add(time.Hour))
	if info == nil {
		t.Fatal("Describe() = nil, want a description")
	}
	if !info.Expired {
		t.Error("Expired = false, want true for a certificate an hour past its notAfter")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil: an expired certificate is reported, not refused", err)
	}
}

func TestDescribeSurvivesAnUnreadableCertificate(t *testing.T) {
	// The read path renders a panel. A row that cannot be described must not
	// stop an administrator from seeing the rest of the listener's state, or
	// from replacing the certificate that caused it.
	cfg := Config{TLSCertPEM: "-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n"}
	if info := cfg.Describe(time.Now()); info != nil {
		t.Errorf("Describe() = %+v, want nil", info)
	}
}
