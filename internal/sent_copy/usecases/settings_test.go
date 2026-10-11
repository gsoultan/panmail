package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/secrets"
)

func newSettings(t *testing.T, withKey bool) *Settings {
	t.Helper()
	var ring *secrets.Keyring
	if withKey {
		key, _ := secrets.GenerateKey()
		ring, _ = secrets.NewKeyring(key)
	}
	return NewSettings(postgres.NewStore(storetest.NewConnection(t), ring), withKey)
}

func shared(password string) *entities.Settings {
	return &entities.Settings{
		Mode: entities.ModeSharedLogin, IMAPHost: "imap.example.com", IMAPUseSSL: true,
		MasterUsername: "admin", MasterPassword: password,
	}
}

func TestABlankMasterPasswordKeepsTheStoredOne(t *testing.T) {
	ctx := context.Background()
	u := newSettings(t, true)
	if _, err := u.Update(ctx, storetest.TenantA, shared("pw")); err != nil {
		t.Fatal(err)
	}
	change := shared("")
	change.SentFolder = "Sent Items"
	got, err := u.Update(ctx, storetest.TenantA, change)
	if err != nil {
		t.Fatalf("Update with a blank password = %v", err)
	}
	if got.MasterPassword != "pw" || got.SentFolder != "Sent Items" {
		t.Errorf("got password %q folder %q", got.MasterPassword, got.SentFolder)
	}

	// Another tenant's stored password is never borrowed.
	if _, err := u.Update(ctx, storetest.TenantB, shared("")); !errors.Is(err, entities.ErrMasterRequired) {
		t.Errorf("tenant B with no password = %v, want ErrMasterRequired", err)
	}
}

func TestLeavingSharedLoginDropsTheMasterPassword(t *testing.T) {
	ctx := context.Background()
	u := newSettings(t, true)
	if _, err := u.Update(ctx, storetest.TenantA, shared("pw")); err != nil {
		t.Fatal(err)
	}
	none := shared("")
	none.Mode = entities.ModeNone
	if _, err := u.Update(ctx, storetest.TenantA, none); err != nil {
		t.Fatal(err)
	}
	got, _ := u.Get(ctx, storetest.TenantA)
	if got.MasterPassword != "" || got.IMAPHost != "imap.example.com" {
		t.Errorf("after None: password %q host %q; want no password, host kept", got.MasterPassword, got.IMAPHost)
	}
	// Coming back needs the password again.
	if _, err := u.Update(ctx, storetest.TenantA, shared("")); !errors.Is(err, entities.ErrMasterRequired) {
		t.Errorf("shared again without a password = %v, want ErrMasterRequired", err)
	}
}

func TestLoginModesNeedADataKey(t *testing.T) {
	ctx := context.Background()
	u := newSettings(t, false)
	if _, err := u.Update(ctx, storetest.TenantA, shared("pw")); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Errorf("shared login without a key = %v", err)
	}
	if _, err := u.SaveMailbox(ctx, storetest.TenantA, &entities.Mailbox{Address: "a@example.com", Password: "pw"}); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Errorf("mailbox without a key = %v", err)
	}
	if _, err := u.Update(ctx, storetest.TenantA, &entities.Settings{Mode: entities.ModeBccSender}); err != nil {
		t.Errorf("bcc to sender without a key = %v, want it allowed", err)
	}
}

func TestMailboxSaveNormalisesAndKeepsThePassword(t *testing.T) {
	ctx := context.Background()
	u := newSettings(t, true)
	if _, err := u.SaveMailbox(ctx, storetest.TenantA, &entities.Mailbox{Address: "x"}); !errors.Is(err, entities.ErrAddressRequired) {
		t.Errorf("bad address = %v", err)
	}
	if _, err := u.SaveMailbox(ctx, storetest.TenantA, &entities.Mailbox{Address: "a@example.com"}); !errors.Is(err, entities.ErrPasswordRequired) {
		t.Errorf("new mailbox without a password = %v", err)
	}
	m, err := u.SaveMailbox(ctx, storetest.TenantA, &entities.Mailbox{Address: "Alice <Alice@Example.COM>", Password: "pw"})
	if err != nil || m.Address != "alice@example.com" {
		t.Fatalf("SaveMailbox = %+v, %v", m, err)
	}
	m, err = u.SaveMailbox(ctx, storetest.TenantA, &entities.Mailbox{Address: "alice@example.com", Username: "alice"})
	if err != nil || m.Password != "pw" || m.Username != "alice" {
		t.Fatalf("edit without a password = %+v, %v", m, err)
	}
	if err := u.DeleteMailbox(ctx, storetest.TenantB, "alice@example.com"); !errors.Is(err, ErrMailboxNotFound) {
		t.Errorf("tenant B deleting A's mailbox = %v", err)
	}
}

func TestValidationRefusesUnusableSettings(t *testing.T) {
	u := newSettings(t, true)
	for name, s := range map[string]*entities.Settings{
		"unknown mode":     {Mode: "sometimes"},
		"no host":          {Mode: entities.ModePerUserLogin},
		"host with scheme": {Mode: entities.ModePerUserLogin, IMAPHost: "imaps://imap.example.com"},
		"bad port":         {Mode: entities.ModePerUserLogin, IMAPHost: "imap.example.com", IMAPPort: 70000},
		"bad separator":    {Mode: entities.ModeSharedLogin, IMAPHost: "h", MasterUsername: "a", MasterPassword: "b", MasterSeparator: "**"},
		"folder injection": {Mode: entities.ModePerUserLogin, IMAPHost: "h", SentFolder: "Sent\r\nX"},
	} {
		if _, err := u.Update(context.Background(), storetest.TenantA, s); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
