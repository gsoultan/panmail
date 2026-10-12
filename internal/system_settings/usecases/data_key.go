package usecases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/secrets"
)

// ErrDataKeyFromEnvironment refuses a key change from the dashboard when the
// key is supplied through PANMAIL_SECRET_KEY. The environment wins at every
// start, so a key written to the config file would be ignored at the next
// restart and every credential re-sealed under it would become unreadable.
var ErrDataKeyFromEnvironment = fmt.Errorf(
	"the data encryption key is set by %s; change it there and run rotate-secrets", secrets.EnvKeyName)

// ErrDataKeyUnchanged is returned when the submitted key is the one in use.
var ErrDataKeyUnchanged = errors.New("that is already the data encryption key in use")

// DataKeySource says where the running key came from.
type DataKeySource int

const (
	DataKeyFromConfig DataKeySource = iota + 1
	DataKeyFromEnvironment
)

// DataKeyStatus describes the key without disclosing it.
type DataKeyStatus struct {
	// KeyID is the non-secret fingerprint stored ciphertext is tagged with.
	KeyID  string
	Source DataKeySource
	// RetiredKeys counts decrypt-only keys still configured. Non-zero after a
	// change means it was interrupted before every value moved.
	RetiredKeys int
}

// DataKeyChange is the outcome of a successful change.
type DataKeyChange struct {
	KeyID string
	// Key is set only when the key was generated, so the administrator can
	// back it up. A key they typed in is never echoed back.
	Key string
	// Rotated counts the stored credentials re-sealed under the new key.
	Rotated int
}

// SecretRotator re-seals one store's encrypted values under the keyring's
// primary key, skipping values already on it.
type SecretRotator interface {
	RotateSecrets(ctx context.Context) (int, error)
}

// DataKeyUsecase reads and replaces the key stored credentials are encrypted
// with.
type DataKeyUsecase interface {
	Status(ctx context.Context) (DataKeyStatus, error)
	// Change moves every stored credential onto newKeyHex, or onto a generated
	// key when it is empty.
	Change(ctx context.Context, newKeyHex string) (DataKeyChange, error)
}

type dataKeyUsecase struct {
	keyring  *secrets.Keyring
	rotators []SecretRotator

	// Both fields so a test can stand in for the config file.
	load func() (*config.Config, error)
	save func(*config.Config) error

	// One change at a time: two interleaved changes would each record the
	// other's new key as retired and then drop it.
	mu sync.Mutex
}

// NewDataKeyUsecase builds the usecase. keyring is the one every store was
// given at boot; rotators must cover every store that holds a sealed value,
// because one left out is one whose rows become unreadable once the old key is
// dropped.
func NewDataKeyUsecase(keyring *secrets.Keyring, rotators ...SecretRotator) DataKeyUsecase {
	return &dataKeyUsecase{keyring: keyring, rotators: rotators, load: config.Load, save: config.Save}
}

func (u *dataKeyUsecase) Status(ctx context.Context) (DataKeyStatus, error) {
	cfg, err := u.load()
	if err != nil {
		return DataKeyStatus{}, err
	}
	status := DataKeyStatus{
		KeyID:       u.keyring.PrimaryKeyID(),
		Source:      DataKeyFromConfig,
		RetiredKeys: len(config.RetiredDataKeys(cfg)),
	}
	if os.Getenv(secrets.EnvKeyName) != "" {
		status.Source = DataKeyFromEnvironment
	}
	return status, nil
}

// Change replaces the key in three steps, ordered so that a failure at any
// point leaves every credential readable:
//
//  1. Write the new key to the config with the current one kept as retired. A
//     restart from here on reads both.
//  2. Move the running keyring onto both keys and re-seal every stored value
//     under the new one.
//  3. Drop the retired key from the config and the keyring.
//
// A failure in step 2 stops with the old key still retired, so nothing is
// lost; running the change again finishes the move.
func (u *dataKeyUsecase) Change(ctx context.Context, newKeyHex string) (DataKeyChange, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if os.Getenv(secrets.EnvKeyName) != "" {
		return DataKeyChange{}, ErrDataKeyFromEnvironment
	}

	cfg, err := u.load()
	if err != nil {
		return DataKeyChange{}, err
	}
	if cfg == nil {
		return DataKeyChange{}, errors.New("panmail is not set up yet")
	}
	current, err := config.ResolveDataKey(cfg)
	if err != nil {
		return DataKeyChange{}, err
	}

	generated := newKeyHex == ""
	if generated {
		if newKeyHex, err = secrets.GenerateKey(); err != nil {
			return DataKeyChange{}, err
		}
	}
	next, err := secrets.NewKeyring(newKeyHex)
	if err != nil {
		return DataKeyChange{}, err
	}
	if next.PrimaryKeyID() == u.keyring.PrimaryKeyID() {
		return DataKeyChange{}, ErrDataKeyUnchanged
	}

	// Step 1.
	retiredInConfig := append([]string{current}, cfg.Secrets.RetiredDataKeys...)
	cfg.Secrets.DataKey = newKeyHex
	cfg.Secrets.RetiredDataKeys = retiredInConfig
	if err := u.save(cfg); err != nil {
		return DataKeyChange{}, fmt.Errorf("failed to save the new key: %w", err)
	}

	// Step 2.
	moving, err := secrets.NewKeyring(newKeyHex, config.RetiredDataKeys(cfg)...)
	if err != nil {
		return DataKeyChange{}, err
	}
	u.keyring.Replace(moving)

	rotated := 0
	for _, r := range u.rotators {
		n, err := r.RotateSecrets(ctx)
		rotated += n
		if err != nil {
			return DataKeyChange{}, fmt.Errorf(
				"moved %d stored credential(s) to the new key, then stopped: %w. "+
					"Nothing is lost: the old key is still kept for reading, and changing the key again finishes the move",
				rotated, err)
		}
	}

	// Step 3.
	cfg.Secrets.RetiredDataKeys = nil
	if err := u.save(cfg); err != nil {
		// Every value is already on the new key; the old one is merely kept
		// longer than needed.
		return DataKeyChange{}, fmt.Errorf("every credential moved to the new key, but the old key could not be removed from the config: %w", err)
	}
	final, err := secrets.NewKeyring(newKeyHex, config.RetiredDataKeys(cfg)...)
	if err != nil {
		return DataKeyChange{}, err
	}
	u.keyring.Replace(final)

	change := DataKeyChange{KeyID: final.PrimaryKeyID(), Rotated: rotated}
	if generated {
		change.Key = newKeyHex
	}
	return change, nil
}
