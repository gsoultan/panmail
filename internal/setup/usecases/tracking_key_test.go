package usecases

import (
	"context"
	"path/filepath"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/pkg/auth"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// Setup generates the instance's symmetric key, and every long-lived thing
// derived from that key has to be moved onto it before setup returns.
//
// The token maker already was. The tracking signer was not, and the difference
// in consequence is why this has its own test: a stale token maker logs someone
// out, whereas a stale tracking signer keeps signing with a key that dies at the
// next restart. Everything sent in between is delivered, unfixable, and answers
// 403 "Invalid tracking link" on every click for as long as the message exists.
func TestSetupMovesTheTrackingSignerOntoTheGeneratedKey(t *testing.T) {
	dir := t.TempDir()
	config.SetConfigPath(filepath.Join(dir, "db_config.yaml"))
	t.Cleanup(func() { config.SetConfigPath("") })

	// Built the way main builds it before setup: no key to derive from yet.
	signer := tracking.NewSigner(nil)

	u := NewSetupUsecase(
		&mockAuthUsecase{isFirstRun: true},
		&mockConnection{},
		auth.NewSwappableTokenMaker(nil),
		signer,
		func(db.Connection, string) error { return nil },
	)

	err := u.Setup(context.Background(), &panmailv1.DatabaseConfig{
		Type:     "sqlite",
		FilePath: filepath.Join(dir, "panmail.db"),
	}, "admin@example.com", "password", "Admin", "https://mail.example.com")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if !signer.HasKey() {
		t.Fatal("setup finished without giving the tracking signer a key: every " +
			"message sent before the next restart carries links that will never verify")
	}

	// The key it was given must be the one a restart derives from the config
	// setup just wrote — that is the whole property.
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("could not read back the config setup wrote: %v", err)
	}
	if saved == nil {
		t.Fatal("setup wrote no config")
	}

	link := tracking.Link{
		Kind:      "click",
		TenantID:  "11111111-1111-1111-1111-111111111111",
		MessageID: "msg-1",
		Recipient: "person+tag@example.org",
		TargetURL: "https://shop.example.com/sale",
	}
	signature := signer.Sign(link)

	afterRestart := tracking.NewSigner(tracking.DeriveKey(saved.Auth.SymmetricKey))
	if err := afterRestart.Verify(link, signature); err != nil {
		t.Fatalf("a link signed right after setup does not verify once panmail "+
			"restarts and reads the key from disk: %v", err)
	}
}
