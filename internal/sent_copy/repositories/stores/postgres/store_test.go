package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/secrets"
)

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

func TestSettingsRoundTripAndStayInTheirTenant(t *testing.T) {
	ctx := context.Background()
	conn := storetest.NewConnection(t)
	s := NewStore(conn, testKeyring(t))

	now := time.Now().UTC().Truncate(time.Second)
	in := &entities.Settings{
		TenantID: storetest.TenantA, Mode: entities.ModeSharedLogin,
		IMAPHost: "imap.example.com", IMAPPort: 993, IMAPUseSSL: true, SentFolder: "Sent Items",
		MasterUsername: "admin", MasterPassword: "s3cret", MasterSeparator: "*", UpdatedAt: now,
	}
	if err := s.SaveSettings(ctx, in); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err := s.GetSettings(ctx, storetest.TenantA)
	if err != nil || got == nil {
		t.Fatalf("GetSettings = %v, %v", got, err)
	}
	if got.Mode != in.Mode || got.IMAPHost != in.IMAPHost || got.SentFolder != in.SentFolder ||
		got.MasterUsername != "admin" || got.MasterPassword != "s3cret" {
		t.Errorf("got %+v, want %+v", got, in)
	}

	var stored string
	if err := conn.GetDB().QueryRow("SELECT master_password FROM sent_copy_settings WHERE tenant_id = $1", storetest.TenantA).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "s3cret" || stored == "" {
		t.Errorf("master password stored as %q, want ciphertext", stored)
	}

	if other, err := s.GetSettings(ctx, storetest.TenantB); err != nil || other != nil {
		t.Errorf("tenant B sees %+v, %v; want nothing", other, err)
	}
}

func TestAPasswordIsNeverStoredWithoutAKey(t *testing.T) {
	s := NewStore(storetest.NewConnection(t), nil)
	err := s.SaveMailbox(context.Background(), &entities.Mailbox{
		TenantID: storetest.TenantA, Address: "a@example.com", Password: "pw", UpdatedAt: time.Now(),
	})
	if !errors.Is(err, ErrNoEncryptionKey) {
		t.Fatalf("SaveMailbox = %v, want ErrNoEncryptionKey", err)
	}
}

func TestMailboxesAreScopedToTheirTenant(t *testing.T) {
	ctx := context.Background()
	s := NewStore(storetest.NewConnection(t), testKeyring(t))
	now := time.Now().UTC()

	if err := s.SaveMailbox(ctx, &entities.Mailbox{TenantID: storetest.TenantA, Address: "a@example.com", Username: "alice", Password: "pw", UpdatedAt: now}); err != nil {
		t.Fatalf("SaveMailbox: %v", err)
	}

	got, err := s.GetMailbox(ctx, storetest.TenantA, "a@example.com")
	if err != nil || got == nil || got.Password != "pw" || got.Username != "alice" {
		t.Fatalf("GetMailbox = %+v, %v", got, err)
	}
	if other, _ := s.GetMailbox(ctx, storetest.TenantB, "a@example.com"); other != nil {
		t.Error("tenant B can read tenant A's mailbox login")
	}
	if removed, _ := s.DeleteMailbox(ctx, storetest.TenantB, "a@example.com"); removed {
		t.Error("tenant B deleted tenant A's mailbox login")
	}
	list, err := s.ListMailboxes(ctx, storetest.TenantA)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListMailboxes = %d, %v", len(list), err)
	}
	if removed, err := s.DeleteMailbox(ctx, storetest.TenantA, "a@example.com"); err != nil || !removed {
		t.Fatalf("DeleteMailbox = %v, %v", removed, err)
	}
}

// Two workers reading the same due job must not both file it.
func TestAJobIsClaimedOnceAndReleasedOnReschedule(t *testing.T) {
	ctx := context.Background()
	s := NewStore(storetest.NewConnection(t), nil)
	now := time.Now()

	job := &entities.Job{ID: storetest.ID("job"), TenantID: storetest.TenantA, MessageID: storetest.ID("msg"), Request: []byte(`{"from":"a@example.com"}`)}
	if err := s.EnqueueJob(ctx, job, now); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	first, err := s.ClaimJobs(ctx, now, time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %d, %v", len(first), err)
	}
	if string(first[0].Request) == "" || first[0].TenantID != storetest.TenantA {
		t.Errorf("claimed job = %+v", first[0])
	}
	if second, _ := s.ClaimJobs(ctx, now, time.Minute, 10); len(second) != 0 {
		t.Fatalf("a claimed job was claimed again")
	}

	// A stale claim may be taken over.
	later := now.Add(2 * time.Minute)
	takeover, _ := s.ClaimJobs(ctx, later, time.Minute, 10)
	if len(takeover) != 1 {
		t.Fatalf("a stale claim was not taken over")
	}

	// The first worker's claim is gone, so its completion does nothing.
	if err := s.CompleteJob(ctx, job.ID, first[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	if err := s.RescheduleJob(ctx, job.ID, takeover[0].ClaimToken, 1, later.Add(time.Hour), "boom"); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.ClaimJobs(ctx, later, time.Minute, 10); len(due) != 0 {
		t.Fatalf("a rescheduled job ran early")
	}
	again, _ := s.ClaimJobs(ctx, later.Add(2*time.Hour), time.Minute, 10)
	if len(again) != 1 || again[0].Attempts != 1 || again[0].LastError != "boom" {
		t.Fatalf("rescheduled job = %+v", again)
	}
	if err := s.CompleteJob(ctx, job.ID, again[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ClaimJobs(ctx, later.Add(3*time.Hour), time.Minute, 10); len(left) != 0 {
		t.Fatal("a completed job is still queued")
	}
}

// Deleting a tenant takes its setting, logins and waiting copies with it, so
// they never make the tenant undeletable.
func TestDeletingATenantTakesItsSentCopiesWithIt(t *testing.T) {
	ctx := context.Background()
	conn := storetest.NewConnection(t)
	s := NewStore(conn, testKeyring(t))
	now := time.Now().UTC()

	if err := s.SaveSettings(ctx, &entities.Settings{TenantID: storetest.TenantA, Mode: entities.ModeBccSender, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMailbox(ctx, &entities.Mailbox{TenantID: storetest.TenantA, Address: "a@example.com", Password: "pw", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueJob(ctx, &entities.Job{ID: storetest.ID("j"), TenantID: storetest.TenantA, MessageID: storetest.ID("m"), Request: []byte(`{}`)}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.GetDB().ExecContext(ctx, "DELETE FROM tenants WHERE id = $1", storetest.TenantA); err != nil {
		t.Fatalf("deleting the tenant: %v", err)
	}
	for _, table := range []string{"sent_copy_settings", "sent_copy_mailboxes", "sent_copy_jobs"} {
		var n int
		if err := conn.GetDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE tenant_id = $1", storetest.TenantA).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows for the deleted tenant", table, n)
		}
	}
}
