package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/secrets"
)

const testPrivateKey = "-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n"

func testKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	key, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ring, err := secrets.NewKeyring(key)
	if err != nil {
		t.Fatalf("build keyring: %v", err)
	}
	return ring
}

// The promise this store makes: a TLS private key is either ciphertext or it is
// not stored. There is no third outcome, and in particular no plaintext
// fallback when the deployment has no data key.
func TestAPrivateKeyIsNeverStoredInTheClear(t *testing.T) {
	store := &store{keyring: testKeyring(t)}

	sealed, err := store.seal(testPrivateKey)
	if err != nil {
		t.Fatalf("seal() = %v, want nil", err)
	}
	if strings.Contains(sealed, "not-a-real-key") {
		t.Fatal("the private key appears verbatim in what would be written to the database")
	}
	if !secrets.IsEncrypted(sealed) {
		t.Errorf("seal() = %q, want a value marked as encrypted", sealed)
	}

	opened, err := store.unseal(sealed)
	if err != nil {
		t.Fatalf("unseal() = %v, want nil", err)
	}
	if opened != testPrivateKey {
		t.Error("the key did not survive the round trip")
	}
}

func TestStoringAKeyWithoutAnEncryptionKeyIsRefused(t *testing.T) {
	store := &store{keyring: nil}

	if _, err := store.seal(testPrivateKey); !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("seal() = %v, want ErrNoEncryptionKey", err)
	}
}

func TestAListenerWithoutTLSNeedsNoEncryptionKey(t *testing.T) {
	// A deployment with no data key can still run a loopback listener, so an
	// empty key must not be turned into an error — or into ciphertext of
	// nothing, which would make "no certificate installed" unreadable from the
	// column itself.
	store := &store{keyring: nil}

	sealed, err := store.seal("")
	if err != nil {
		t.Fatalf("seal(\"\") = %v, want nil", err)
	}
	if sealed != "" {
		t.Errorf("seal(\"\") = %q, want an empty column", sealed)
	}
}

// Failing closed. Serving without TLS because the ciphertext could not be
// opened would silently downgrade a listener the operator configured to be
// encrypted.
func TestAnUnreadableStoredKeyIsAnErrorNotAnEmptyResult(t *testing.T) {
	sealed, err := (&store{keyring: testKeyring(t)}).seal(testPrivateKey)
	if err != nil {
		t.Fatalf("seal() = %v, want nil", err)
	}

	testCases := []struct {
		name    string
		store   *store
		wantErr error
	}{
		{
			name:    "no key at all",
			store:   &store{keyring: nil},
			wantErr: ErrNoEncryptionKey,
		},
		{
			name:  "the wrong key",
			store: &store{keyring: testKeyring(t)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opened, err := tc.store.unseal(sealed)
			if err == nil {
				t.Fatalf("unseal() = %q, want an error", opened)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("unseal() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func newStore(t *testing.T, keyring *secrets.Keyring) *store {
	t.Helper()
	return NewStore(storetest.NewConnection(t), keyring).(*store)
}

func TestGetBeforeAnythingIsStored(t *testing.T) {
	// Not an error and not a zeroed row: the caller has to be able to tell
	// "nobody has configured a listener" from "somebody configured one that is
	// off", because the first resolves to the defaults.
	got, err := newStore(t, testKeyring(t)).Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("Get on an empty table = %+v; want nil", got)
	}
}

func TestSaveRoundTripsEveryField(t *testing.T) {
	store := newStore(t, testKeyring(t))

	want := &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeAllInterfaces,
		Port:              2525,
		TLSCertPEM:        "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n",
		TLSKeyPEM:         testPrivateKey,
		AllowInsecureAuth: false,
		UpdatedAt:         time.Now().UTC().Truncate(time.Second),
	}
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Save = nil")
	}

	if got.Enabled != want.Enabled ||
		got.BindScope != want.BindScope ||
		got.Port != want.Port ||
		got.AllowInsecureAuth != want.AllowInsecureAuth {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
	if got.TLSCertPEM != want.TLSCertPEM {
		t.Error("the certificate did not survive the round trip")
	}
	// The point of the whole exercise: the key comes back usable, having been
	// stored sealed.
	if got.TLSKeyPEM != want.TLSKeyPEM {
		t.Error("the private key did not survive the round trip")
	}
}

// The row is written to the database sealed, not merely handled sealed in Go.
// Reading the column directly is the only way to assert that.
func TestTheStoredColumnHoldsCiphertext(t *testing.T) {
	conn := storetest.NewConnection(t)
	store := NewStore(conn, testKeyring(t))

	err := store.Save(t.Context(), &entities.Config{
		Enabled:    true,
		BindScope:  entities.BindScopeAllInterfaces,
		Port:       587,
		TLSCertPEM: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n",
		TLSKeyPEM:  testPrivateKey,
		UpdatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	var stored string
	row := conn.GetDB().QueryRowContext(t.Context(), "SELECT tls_private_key FROM smtp_submission WHERE id = 1")
	if err := row.Scan(&stored); err != nil {
		t.Fatalf("read the column back: %v", err)
	}

	if strings.Contains(stored, "not-a-real-key") {
		t.Fatal("the private key is in the database in the clear")
	}
	if !secrets.IsEncrypted(stored) {
		t.Errorf("tls_private_key = %q, want a value marked as encrypted", stored)
	}
}

func TestSavingACertificateWithoutADataKeyIsRefusedBeforeTheDatabaseIsTouched(t *testing.T) {
	store := newStore(t, nil)

	err := store.Save(t.Context(), &entities.Config{
		Enabled:    true,
		BindScope:  entities.BindScopeAllInterfaces,
		Port:       587,
		TLSCertPEM: "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n",
		TLSKeyPEM:  testPrivateKey,
		UpdatedAt:  time.Now().UTC(),
	})
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("Save = %v, want ErrNoEncryptionKey", err)
	}

	// And nothing was written: a refused save must not leave a half-configured
	// listener behind.
	got, err := store.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("Get after a refused Save = %+v, want nil", got)
	}
}

// A listener with no TLS is exactly what a deployment without a data key is
// still allowed to run.
func TestALoopbackListenerRoundTripsWithoutADataKey(t *testing.T) {
	store := newStore(t, nil)

	want := &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeLoopback,
		Port:              587,
		AllowInsecureAuth: true,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || !got.Enabled || got.Port != 587 || !got.AllowInsecureAuth {
		t.Fatalf("Get = %+v, want the stored loopback listener", got)
	}
}

func TestSaveOverwritesRatherThanAccumulating(t *testing.T) {
	// One row, enforced by the CHECK in the migration. A second Save must
	// update it, not fail on the primary key.
	store := newStore(t, testKeyring(t))

	for _, port := range []int{587, 2525} {
		err := store.Save(t.Context(), &entities.Config{
			Enabled:           true,
			BindScope:         entities.BindScopeLoopback,
			Port:              port,
			AllowInsecureAuth: true,
			UpdatedAt:         time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("Save(port %d): %v", port, err)
		}
	}

	got, err := store.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Port != 2525 {
		t.Errorf("Port = %d, want the second save to have replaced the first", got.Port)
	}
}
