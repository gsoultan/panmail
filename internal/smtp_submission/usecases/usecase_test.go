package usecases

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
)

// testTLSMaterial returns a self-signed certificate and its key, both PEM.
// The merge rules are about not losing this, so it has to be real enough to
// survive validation.
func testTLSMaterial(t *testing.T) (string, string) {
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

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

// fakeRepo is the stored row, in memory.
type fakeRepo struct {
	config  *entities.Config
	saved   int
	saveErr error
	getErr  error
}

func (f *fakeRepo) Get(context.Context) (*entities.Config, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.config == nil {
		return nil, nil
	}
	clone := *f.config
	return &clone, nil
}

func (f *fakeRepo) Save(_ context.Context, cfg *entities.Config) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	clone := *cfg
	f.config = &clone
	f.saved++
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// storedTLS is a config carrying a keypair, used to prove the merge rules do
// not lose it.
func storedTLS(cert, key string) *entities.Config {
	return &entities.Config{
		Enabled:    true,
		BindScope:  entities.BindScopeAllInterfaces,
		Port:       2525,
		TLSCertPEM: cert,
		TLSKeyPEM:  key,
	}
}

func TestUpdateKeepsAStoredKeypairWhenTheFormDoesNotResendIt(t *testing.T) {
	// The rule that lets an administrator change the port without pasting a
	// private key back into a browser. Getting this wrong deletes a working
	// certificate on every unrelated save.
	cert, key := testTLSMaterial(t)
	repo := &fakeRepo{config: storedTLS(cert, key)}
	usecase := NewUsecase(repo, nil, nil, true, 0, discardLogger())

	_, err := usecase.Update(context.Background(), Change{
		Enabled:   true,
		BindScope: entities.BindScopeAllInterfaces,
		Port:      587,
	})
	if err != nil {
		t.Fatalf("Update() = %v, want nil", err)
	}

	if repo.config.TLSCertPEM != cert || repo.config.TLSKeyPEM != key {
		t.Error("the stored keypair was lost by a save that did not mention it")
	}
	if repo.config.Port != 587 {
		t.Errorf("Port = %d, want 587", repo.config.Port)
	}
}

func TestUpdateTLSRules(t *testing.T) {
	cert, key := testTLSMaterial(t)

	testCases := []struct {
		name       string
		change     Change
		wantErr    error
		wantCert   string
		wantKeyLen bool
	}{
		{
			name: "clearing removes the stored pair",
			change: Change{
				Enabled: true, BindScope: entities.BindScopeLoopback, Port: 587,
				AllowInsecureAuth: true, ClearTLS: true,
			},
			wantCert: "",
		},
		{
			name: "clearing and supplying at once is refused",
			change: Change{
				Enabled: true, BindScope: entities.BindScopeAllInterfaces, Port: 587,
				TLSCertPEM: cert, TLSKeyPEM: key, ClearTLS: true,
			},
			wantErr: ErrClearAndSetTLS,
		},
		{
			name: "a certificate without a key is refused",
			change: Change{
				Enabled: true, BindScope: entities.BindScopeAllInterfaces, Port: 587,
				TLSCertPEM: cert,
			},
			wantErr: ErrTLSPairRequired,
		},
		{
			name: "a key without a certificate is refused",
			change: Change{
				Enabled: true, BindScope: entities.BindScopeAllInterfaces, Port: 587,
				TLSKeyPEM: key,
			},
			wantErr: ErrTLSPairRequired,
		},
		{
			name: "a complete pair replaces the stored one",
			change: Change{
				Enabled: true, BindScope: entities.BindScopeAllInterfaces, Port: 587,
				TLSCertPEM: cert, TLSKeyPEM: key,
			},
			wantCert: cert,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{config: storedTLS(cert, key)}
			usecase := NewUsecase(repo, nil, nil, true, 0, discardLogger())

			_, err := usecase.Update(context.Background(), tc.change)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Update() = %v, want %v", err, tc.wantErr)
				}
				if repo.saved != 0 {
					t.Error("a refused change was stored anyway")
				}
				return
			}
			if err != nil {
				t.Fatalf("Update() = %v, want nil", err)
			}
			if repo.config.TLSCertPEM != tc.wantCert {
				t.Errorf("stored certificate = %q, want %q", repo.config.TLSCertPEM, tc.wantCert)
			}
		})
	}
}

func TestUpdateRefusesToWidenExposureWithoutTLS(t *testing.T) {
	// End to end through the usecase, because the guard being in the entity is
	// only useful if every write path actually reaches it.
	repo := &fakeRepo{}
	usecase := NewUsecase(repo, nil, nil, true, 0, discardLogger())

	_, err := usecase.Update(context.Background(), Change{
		Enabled:           true,
		BindScope:         entities.BindScopeAllInterfaces,
		Port:              587,
		AllowInsecureAuth: true,
	})
	if !errors.Is(err, entities.ErrInsecureAuthOffLoopback) {
		t.Fatalf("Update() = %v, want ErrInsecureAuthOffLoopback", err)
	}
	if repo.saved != 0 {
		t.Error("a configuration that would leak API keys was stored")
	}
}

func TestFlagsWinOverTheStoredConfiguration(t *testing.T) {
	repo := &fakeRepo{config: storedTLS(testTLSMaterial(t))}
	flags := &FlagListener{Host: "mail.example.com", Port: 587, STARTTLS: true}
	usecase := NewUsecase(repo, nil, flags, true, 0, discardLogger())

	snapshot, err := usecase.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe() = %v, want nil", err)
	}
	if !snapshot.ManagedByFlags || snapshot.Editable {
		t.Errorf("snapshot = %+v, want managed by flags and not editable", snapshot)
	}
	if snapshot.Host != "mail.example.com" || snapshot.Port != 587 {
		t.Errorf("snapshot reported %s:%d, want the flag listener", snapshot.Host, snapshot.Port)
	}

	if _, err := usecase.Update(context.Background(), Change{Enabled: false}); !errors.Is(err, ErrManagedByFlags) {
		t.Fatalf("Update() = %v, want ErrManagedByFlags", err)
	}
	if repo.saved != 0 {
		t.Error("a flag-managed gateway stored a change it will never apply")
	}
}

func TestDescribeWithdrawsAWildcardHost(t *testing.T) {
	// 0.0.0.0 names nothing a client can dial. Reporting it would send an
	// integrator somewhere that does not answer; the dashboard falls back to
	// base_url instead.
	cert, key := testTLSMaterial(t)
	repo := &fakeRepo{config: storedTLS(cert, key)}
	usecase := NewUsecase(repo, nil, nil, true, 0, discardLogger())

	snapshot, err := usecase.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe() = %v, want nil", err)
	}
	if snapshot.Host != "" {
		t.Errorf("Host = %q, want empty for a wildcard bind", snapshot.Host)
	}
	if snapshot.Certificate == nil {
		t.Error("Certificate = nil, want the stored certificate described")
	}
}

func TestDescribeSaysWhyItCannotBeEdited(t *testing.T) {
	// Without a data key a certificate cannot be stored, so the panel says so
	// up front rather than failing at save.
	usecase := NewUsecase(&fakeRepo{}, nil, nil, false, 0, discardLogger())

	snapshot, err := usecase.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe() = %v, want nil", err)
	}
	if snapshot.Editable {
		t.Error("Editable = true, want false with no data encryption key")
	}
	if snapshot.NotEditableReason == "" {
		t.Error("NotEditableReason is empty, want an explanation")
	}
}

func TestAStoredChangeIsReportedEvenWhenItCannotBind(t *testing.T) {
	// Storing before applying is what makes this the recoverable half-state:
	// the reconcile loop retries it, and the caller is told why it is not
	// serving rather than being told the save failed.
	port := freePort(t)
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy the port: %v", err)
	}
	defer occupier.Close()

	repo := &fakeRepo{}
	supervisor := newTestSupervisor(t)
	usecase := NewUsecase(repo, supervisor, nil, true, 0, discardLogger())

	snapshot, err := usecase.Update(context.Background(), Change{
		Enabled:           true,
		BindScope:         entities.BindScopeLoopback,
		Port:              port,
		AllowInsecureAuth: true,
	})
	if err != nil {
		t.Fatalf("Update() = %v, want nil: the change was accepted and stored", err)
	}
	if repo.saved != 1 {
		t.Errorf("saved = %d, want the change stored despite the bind failure", repo.saved)
	}
	if snapshot.LastError == "" {
		t.Error("LastError is empty, want the bind failure reported to the caller")
	}
	if !snapshot.Enabled {
		t.Error("Enabled = false, want the administrator's setting reflected back")
	}
}

func TestReconcileAppliesTheStoredConfiguration(t *testing.T) {
	port := freePort(t)
	repo := &fakeRepo{config: &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeLoopback,
		Port:              port,
		AllowInsecureAuth: true,
	}}
	supervisor := newTestSupervisor(t)
	usecase := NewUsecase(repo, supervisor, nil, true, 0, discardLogger())

	if err := usecase.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() = %v, want nil", err)
	}
	assertAccepting(t, port)
}

func TestReconcileRefusesAnUnusableStoredRow(t *testing.T) {
	// A row edited by hand into something this build rejects must not be
	// applied, and must not tear down a listener that is already serving.
	repo := &fakeRepo{config: &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeAllInterfaces,
		Port:              2525,
		AllowInsecureAuth: true,
	}}
	usecase := NewUsecase(repo, newTestSupervisor(t), nil, true, 0, discardLogger())

	if err := usecase.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() = nil, want a refusal for a row that would leak API keys")
	}
}

func TestReconcileIntervalIsBounded(t *testing.T) {
	// The flag is operator input, so the two ways of getting it wrong both have
	// a defined answer: unset takes the default, and a value short enough to
	// turn the safety net into load against the settings row is raised rather
	// than honoured.
	testCases := []struct {
		name      string
		requested time.Duration
		want      time.Duration
	}{
		{name: "unset takes the default", requested: 0, want: DefaultReconcileInterval},
		{name: "negative takes the default", requested: -time.Minute, want: DefaultReconcileInterval},
		{name: "below the floor is raised", requested: time.Millisecond, want: MinReconcileInterval},
		{name: "the floor itself is honoured", requested: MinReconcileInterval, want: MinReconcileInterval},
		{name: "a longer interval is honoured", requested: 5 * time.Minute, want: 5 * time.Minute},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			u := NewUsecase(&fakeRepo{}, nil, nil, true, tc.requested, discardLogger()).(*usecase)
			if u.reconcileInterval != tc.want {
				t.Errorf("reconcileInterval = %v, want %v", u.reconcileInterval, tc.want)
			}
		})
	}
}

// Run has to honour the configured interval, not the default: a test that only
// checked the stored field would pass while Run went on using a constant.
func TestRunReconcilesOnTheConfiguredInterval(t *testing.T) {
	port := freePort(t)
	repo := &fakeRepo{config: &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeLoopback,
		Port:              port,
		AllowInsecureAuth: true,
	}}
	supervisor := newTestSupervisor(t)
	usecase := NewUsecase(repo, supervisor, nil, true, MinReconcileInterval, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go usecase.Run(ctx)

	// One second is the floor, so a listener that is never started within a few
	// of them means Run is not ticking on what it was given.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if state := supervisor.State(); state.Config != nil {
			assertAccepting(t, port)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not reconcile within 10s on a one-second interval")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
