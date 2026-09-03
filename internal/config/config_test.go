package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryption(t *testing.T) {
	key := hex.EncodeToString(make([]byte, 32)) // dummy key
	password := "my-secret-password"

	encrypted, err := encrypt(password, key)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	if encrypted == password {
		t.Errorf("encrypted password should be different from plain text")
	}

	decrypted, err := decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if decrypted != password {
		t.Errorf("decrypted password mismatch: got %s, want %s", decrypted, password)
	}
}

// A Kubernetes Secret volume — which is how deploy/kubernetes/panmail.yaml
// mounts config.yaml — is always read-only. The settings page no longer writes
// here (those values moved to the database), but the setup wizard still does,
// and a first run against a read-only config directory has to say so.
//
// It must be an error. A save that appears to succeed and changes nothing is
// the failure an operator cannot see.
func TestSaveOnAReadOnlyMountRefusesRatherThanLosingTheChange(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}

	mount := filepath.Join(t.TempDir(), "etc-panmail")
	if err := os.MkdirAll(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mount, "config.yaml")
	if err := os.WriteFile(path, []byte("app:\n  base_url: https://a.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	SetConfigPath(path)
	t.Cleanup(func() { SetConfigPath("") })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("precondition: Load: %v", err)
	}

	// Writable first. Without this the read-only assertion below could pass
	// because Save never works here for some unrelated reason.
	cfg.App.BaseURL = "https://b.example"
	if err := Save(cfg); err != nil {
		t.Fatalf("precondition: Save on a writable directory: %v", err)
	}

	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mount, 0o555); err != nil {
		t.Fatal(err)
	}
	// Restore before TempDir's own cleanup, which cannot remove a 0555 dir.
	t.Cleanup(func() { _ = os.Chmod(mount, 0o700) })

	cfg.App.BaseURL = "https://c.example"
	if err := Save(cfg); err == nil {
		t.Fatal("Save reported success on a read-only mount; a settings change would be lost with nothing to show for it")
	}

	reread, err := Load()
	if err != nil {
		t.Fatalf("Load after the refused Save: %v", err)
	}
	if reread.App.BaseURL != "https://b.example" {
		t.Fatalf("the file moved despite the error: base_url = %q, want the pre-Save value", reread.App.BaseURL)
	}
}
