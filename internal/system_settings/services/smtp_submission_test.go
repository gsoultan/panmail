package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	smtpentities "github.com/gsoultan/panmail/internal/smtp_submission/entities"
	smtpstore "github.com/gsoultan/panmail/internal/smtp_submission/repositories/stores/postgres"
	smtpusecases "github.com/gsoultan/panmail/internal/smtp_submission/usecases"
)

type fakeSubmissionUsecase struct {
	snapshot *smtpusecases.Snapshot
	err      error
	got      smtpusecases.Change
}

func (f *fakeSubmissionUsecase) Describe(context.Context) (*smtpusecases.Snapshot, error) {
	return f.snapshot, f.err
}

func (f *fakeSubmissionUsecase) Update(_ context.Context, change smtpusecases.Change) (*smtpusecases.Snapshot, error) {
	f.got = change
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}

func (f *fakeSubmissionUsecase) Reconcile(context.Context) error { return nil }
func (f *fakeSubmissionUsecase) Run(context.Context)             {}

// realCertificate returns a certificate and the key that signed it, so the
// disclosure test has actual key material to look for.
func realCertificate(t *testing.T) (*smtpentities.CertificateInfo, string) {
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
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))

	cfg := smtpentities.Config{TLSCertPEM: certPEM, TLSKeyPEM: keyPEM}
	info := cfg.Describe(time.Now())
	if info == nil {
		t.Fatal("Describe() = nil, want the certificate described")
	}
	return info, keyPEM
}

// The property the whole design rests on: a private key cannot come back out
// through this API. Asserted against the encoded message rather than field by
// field, so a field added later that happens to carry key material fails here
// instead of shipping.
func TestNoResponseCanCarryAPrivateKey(t *testing.T) {
	certificate, privateKey := realCertificate(t)

	service := &settingsService{
		smtpSubmission: &fakeSubmissionUsecase{snapshot: &smtpusecases.Snapshot{
			Enabled:     true,
			Host:        "mail.example.test",
			Port:        587,
			STARTTLS:    true,
			BindScope:   smtpentities.BindScopeAllInterfaces,
			Certificate: certificate,
			Editable:    true,
		}},
	}

	res, err := service.UpdateSmtpSubmission(context.Background(),
		connect.NewRequest(&panmailv1.UpdateSmtpSubmissionRequest{
			Config: &panmailv1.SmtpSubmissionConfig{
				Enabled:           true,
				BindScope:         panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_ALL_INTERFACES,
				Port:              587,
				TlsPrivateKeyPem:  privateKey,
				TlsCertificatePem: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n",
			},
		}))
	if err != nil {
		t.Fatalf("UpdateSmtpSubmission: %v", err)
	}

	encoded, err := proto.Marshal(res.Msg)
	if err != nil {
		t.Fatalf("marshal the response: %v", err)
	}

	for _, needle := range []string{privateKey, "PRIVATE KEY"} {
		if strings.Contains(string(encoded), needle) {
			t.Fatalf("the response carries %q", needle)
		}
	}

	// The metadata that is supposed to come back, does.
	got := res.Msg.GetSmtpSubmission()
	if got.GetCertificate().GetFingerprintSha256() != certificate.FingerprintSHA256 {
		t.Error("the certificate fingerprint was not reported")
	}
	if got.GetCertificate().GetSubject() == "" {
		t.Error("the certificate subject was not reported")
	}
}

func TestTheRequestReachesTheUsecaseIntact(t *testing.T) {
	fake := &fakeSubmissionUsecase{snapshot: &smtpusecases.Snapshot{}}
	service := &settingsService{smtpSubmission: fake}

	_, err := service.UpdateSmtpSubmission(context.Background(),
		connect.NewRequest(&panmailv1.UpdateSmtpSubmissionRequest{
			Config: &panmailv1.SmtpSubmissionConfig{
				Enabled:           true,
				BindScope:         panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_LOOPBACK,
				Port:              2525,
				AllowInsecureAuth: true,
				ClearTls:          true,
			},
		}))
	if err != nil {
		t.Fatalf("UpdateSmtpSubmission: %v", err)
	}

	want := smtpusecases.Change{
		Enabled:           true,
		BindScope:         smtpentities.BindScopeLoopback,
		Port:              2525,
		AllowInsecureAuth: true,
		ClearTLS:          true,
	}
	if fake.got != want {
		t.Errorf("the usecase received %+v, want %+v", fake.got, want)
	}
}

// An unspecified scope resolves to loopback rather than being rejected: it is
// what a client that has not chosen sends, and the unreachable-from-elsewhere
// scope is the safe reading.
func TestAnUnspecifiedBindScopeResolvesToLoopback(t *testing.T) {
	fake := &fakeSubmissionUsecase{snapshot: &smtpusecases.Snapshot{}}
	service := &settingsService{smtpSubmission: fake}

	_, err := service.UpdateSmtpSubmission(context.Background(),
		connect.NewRequest(&panmailv1.UpdateSmtpSubmissionRequest{
			Config: &panmailv1.SmtpSubmissionConfig{Port: 587, AllowInsecureAuth: true},
		}))
	if err != nil {
		t.Fatalf("UpdateSmtpSubmission: %v", err)
	}
	if fake.got.BindScope != smtpentities.BindScopeLoopback {
		t.Errorf("BindScope = %q, want loopback", fake.got.BindScope)
	}
}

func TestSubmissionErrorCodes(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{
			// Not fixable by sending different input: it needs a flag removed.
			name: "flag-managed is a precondition",
			err:  smtpusecases.ErrManagedByFlags,
			want: connect.CodeFailedPrecondition,
		},
		{
			// Needs a key configured on the gateway, not a different request.
			name: "a missing data key is a precondition",
			err:  smtpstore.ErrNoEncryptionKey,
			want: connect.CodeFailedPrecondition,
		},
		{
			name: "insecure auth off loopback is a bad request",
			err:  smtpentities.ErrInsecureAuthOffLoopback,
			want: connect.CodeInvalidArgument,
		},
		{name: "the relay port is a bad request", err: smtpentities.ErrRelayPort, want: connect.CodeInvalidArgument},
		{name: "a missing certificate is a bad request", err: smtpentities.ErrTLSRequired, want: connect.CodeInvalidArgument},
		{name: "an incomplete pair is a bad request", err: smtpentities.ErrIncompleteTLS, want: connect.CodeInvalidArgument},
		{name: "no auth path is a bad request", err: smtpentities.ErrNoAuthPath, want: connect.CodeInvalidArgument},
		{name: "a half-instruction is a bad request", err: smtpusecases.ErrClearAndSetTLS, want: connect.CodeInvalidArgument},
		{name: "anything else is internal", err: errors.New("the database fell over"), want: connect.CodeInternal},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connect.CodeOf(submissionError(tc.err)); got != tc.want {
				t.Errorf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

// Wrapping must not lose the mapping: the usecase wraps validation failures on
// the way out, and errors.Is is what keeps the code correct through that.
func TestAWrappedDomainErrorKeepsItsCode(t *testing.T) {
	wrapped := errors.Join(errors.New("applying the stored configuration"), smtpentities.ErrRelayPort)
	if got := connect.CodeOf(submissionError(wrapped)); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestAMissingConfigIsRefused(t *testing.T) {
	service := &settingsService{smtpSubmission: &fakeSubmissionUsecase{snapshot: &smtpusecases.Snapshot{}}}

	_, err := service.UpdateSmtpSubmission(context.Background(),
		connect.NewRequest(&panmailv1.UpdateSmtpSubmissionRequest{}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// A gateway built without submission still serves its settings page.
func TestAnAbsentUsecaseDescribesADisabledListener(t *testing.T) {
	service := &settingsService{}

	got, err := service.describeSubmission(context.Background())
	if err != nil {
		t.Fatalf("describeSubmission: %v", err)
	}
	if got.GetEnabled() {
		t.Error("Enabled = true, want false with no submission usecase")
	}

	if _, err := service.UpdateSmtpSubmission(context.Background(),
		connect.NewRequest(&panmailv1.UpdateSmtpSubmissionRequest{
			Config: &panmailv1.SmtpSubmissionConfig{},
		})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("code = %v, want Unimplemented", connect.CodeOf(err))
	}
}

func TestManagedByFlagsIsReportedToTheDashboard(t *testing.T) {
	service := &settingsService{smtpSubmission: &fakeSubmissionUsecase{snapshot: &smtpusecases.Snapshot{
		Enabled:           true,
		Host:              "mail.example.test",
		Port:              587,
		STARTTLS:          true,
		ManagedByFlags:    true,
		Editable:          false,
		NotEditableReason: "configured by process flags",
	}}}

	got, err := service.describeSubmission(context.Background())
	if err != nil {
		t.Fatalf("describeSubmission: %v", err)
	}
	if !got.GetManagedByFlags() || got.GetEditable() {
		t.Errorf("got %+v, want managed by flags and not editable", got)
	}
	if got.GetNotEditableReason() == "" {
		t.Error("NotEditableReason is empty, want the reason shown to the administrator")
	}
}
