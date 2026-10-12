package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/secrets"
)

// sealedStore holds one encrypted value and rotates it the way the real
// stores do.
type sealedStore struct {
	keyring *secrets.Keyring
	value   string
	fail    error
}

func (s *sealedStore) RotateSecrets(context.Context) (int, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	if !s.keyring.NeedsRotation(s.value) {
		return 0, nil
	}
	plain, err := s.keyring.Decrypt(s.value)
	if err != nil {
		return 0, err
	}
	if s.value, err = s.keyring.Encrypt(plain); err != nil {
		return 0, err
	}
	return 1, nil
}

type dataKeyFixture struct {
	u       *dataKeyUsecase
	store   *sealedStore
	saved   *config.Config
	oldKey  string
	keyring *secrets.Keyring
}

func newDataKeyFixture(t *testing.T) *dataKeyFixture {
	t.Helper()
	t.Setenv(secrets.EnvKeyName, "")
	t.Setenv(secrets.EnvRetiredKeysName, "")

	oldKey, _ := secrets.GenerateKey()
	keyring, err := secrets.NewKeyring(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := keyring.Encrypt("smtp-password")
	if err != nil {
		t.Fatal(err)
	}

	f := &dataKeyFixture{
		store:   &sealedStore{keyring: keyring, value: sealed},
		saved:   &config.Config{Secrets: config.SecretsConfig{DataKey: oldKey}},
		oldKey:  oldKey,
		keyring: keyring,
	}
	u := NewDataKeyUsecase(keyring, f.store).(*dataKeyUsecase)
	u.load = func() (*config.Config, error) {
		copied := *f.saved
		copied.Secrets.RetiredDataKeys = append([]string(nil), f.saved.Secrets.RetiredDataKeys...)
		return &copied, nil
	}
	u.save = func(c *config.Config) error {
		copied := *c
		copied.Secrets.RetiredDataKeys = append([]string(nil), c.Secrets.RetiredDataKeys...)
		f.saved = &copied
		return nil
	}
	f.u = u
	return f
}

// What a restart would build from the saved config.
func (f *dataKeyFixture) afterRestart(t *testing.T) *secrets.Keyring {
	t.Helper()
	k, err := secrets.NewKeyring(f.saved.Secrets.DataKey, config.RetiredDataKeys(f.saved)...)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestChangeDataKeyGeneratesAKeyAndMovesEveryCredentialOntoIt(t *testing.T) {
	f := newDataKeyFixture(t)

	change, err := f.u.Change(context.Background(), "")
	if err != nil {
		t.Fatalf("change failed: %v", err)
	}
	if change.Key == "" || change.Key == f.oldKey {
		t.Fatal("a generated key must be returned once so it can be backed up")
	}
	if change.Rotated != 1 {
		t.Fatalf("rotated %d, want 1", change.Rotated)
	}
	if f.saved.Secrets.DataKey != change.Key || len(f.saved.Secrets.RetiredDataKeys) != 0 {
		t.Fatalf("config holds key %q with %d retired; want the new key and none retired",
			f.saved.Secrets.DataKey, len(f.saved.Secrets.RetiredDataKeys))
	}

	// The old key is gone everywhere, and the credential still reads.
	for name, k := range map[string]*secrets.Keyring{"running": f.keyring, "after restart": f.afterRestart(t)} {
		if got, err := k.Decrypt(f.store.value); err != nil || got != "smtp-password" {
			t.Fatalf("%s: credential unreadable after the change: %q, %v", name, got, err)
		}
		if k.PrimaryKeyID() != change.KeyID {
			t.Fatalf("%s: keyring is on %s, want %s", name, k.PrimaryKeyID(), change.KeyID)
		}
	}
}

func TestChangeDataKeyNeverEchoesASuppliedKey(t *testing.T) {
	f := newDataKeyFixture(t)
	supplied, _ := secrets.GenerateKey()

	change, err := f.u.Change(context.Background(), supplied)
	if err != nil {
		t.Fatal(err)
	}
	if change.Key != "" {
		t.Fatal("a key the administrator typed was sent back in the response")
	}
	if f.saved.Secrets.DataKey != supplied {
		t.Fatal("the supplied key was not saved")
	}
}

// A store that fails mid-change must leave everything readable, including
// after a restart, and a second attempt must finish the move.
func TestInterruptedDataKeyChangeLosesNothing(t *testing.T) {
	f := newDataKeyFixture(t)
	f.store.fail = errors.New("database went away")

	if _, err := f.u.Change(context.Background(), ""); err == nil {
		t.Fatal("expected the change to report the failure")
	}
	if len(f.saved.Secrets.RetiredDataKeys) == 0 {
		t.Fatal("the old key was dropped before every credential had moved")
	}
	if got, err := f.afterRestart(t).Decrypt(f.store.value); err != nil || got != "smtp-password" {
		t.Fatalf("credential unreadable after a restart mid-change: %q, %v", got, err)
	}

	f.store.fail = nil
	if _, err := f.u.Change(context.Background(), ""); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if len(f.saved.Secrets.RetiredDataKeys) != 0 {
		t.Fatal("retired keys left behind after a successful retry")
	}
	if got, err := f.afterRestart(t).Decrypt(f.store.value); err != nil || got != "smtp-password" {
		t.Fatalf("credential unreadable after the retry: %q, %v", got, err)
	}
}

// With PANMAIL_SECRET_KEY set, a key written to the config would be ignored at
// the next start, and every credential sealed under it lost.
func TestChangeDataKeyIsRefusedWhenTheEnvironmentSetsTheKey(t *testing.T) {
	f := newDataKeyFixture(t)
	t.Setenv(secrets.EnvKeyName, f.oldKey)

	if _, err := f.u.Change(context.Background(), ""); !errors.Is(err, ErrDataKeyFromEnvironment) {
		t.Fatalf("got %v, want ErrDataKeyFromEnvironment", err)
	}
	if f.saved.Secrets.DataKey != f.oldKey {
		t.Fatal("the config was changed anyway")
	}
	status, err := f.u.Status(context.Background())
	if err != nil || status.Source != DataKeyFromEnvironment {
		t.Fatalf("status = %+v, %v; want source environment", status, err)
	}
}

func TestChangeDataKeyRejectsAMalformedOrUnchangedKey(t *testing.T) {
	f := newDataKeyFixture(t)

	if _, err := f.u.Change(context.Background(), "not-hex"); !errors.Is(err, secrets.ErrInvalidKey) {
		t.Fatalf("got %v, want ErrInvalidKey", err)
	}
	if _, err := f.u.Change(context.Background(), f.oldKey); !errors.Is(err, ErrDataKeyUnchanged) {
		t.Fatalf("got %v, want ErrDataKeyUnchanged", err)
	}
	if f.saved.Secrets.DataKey != f.oldKey {
		t.Fatal("a refused change still touched the config")
	}
}
