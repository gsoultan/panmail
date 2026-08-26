package pebble

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

const retentionTenant = "tenant-a"

func newRetentionStore(t *testing.T) stores.EventRepository {
	t.Helper()

	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// writeMessage stores a message and waits for the async writer to flush it,
// so a prune that follows sees it.
func writeMessage(t *testing.T, s stores.EventRepository, m *entities.EmailMessage) {
	t.Helper()

	if err := s.WriteMessage(t.Context(), m); err != nil {
		t.Fatalf("failed to write message %s: %v", m.ID, err)
	}
	waitForMessage(t, s, m.ID)
}

func waitForMessage(t *testing.T, s stores.EventRepository, id string) {
	t.Helper()

	for range 100 {
		if got, _ := s.GetMessage(t.Context(), retentionTenant, id); got != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("message %s never became readable", id)
}

func message(id string, age time.Duration) *entities.EmailMessage {
	return &entities.EmailMessage{
		ID:        id,
		TenantID:  retentionTenant,
		To:        []string{"to@example.com"},
		Cc:        []string{"cc@example.com"},
		Subject:   "subject " + id,
		BodyHTML:  "<p>body</p>",
		CreatedAt: time.Now().Add(-age),
	}
}

func TestTruncateMessagesBefore(t *testing.T) {
	tests := []struct {
		name        string
		age         time.Duration
		cutoff      time.Duration
		wantRemoved int64
	}{
		{name: "older than the cutoff goes", age: 48 * time.Hour, cutoff: 24 * time.Hour, wantRemoved: 1},
		{name: "newer than the cutoff stays", age: time.Hour, cutoff: 24 * time.Hour, wantRemoved: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newRetentionStore(t)
			writeMessage(t, s, message("m1", tc.age))

			removed, err := s.TruncateMessagesBefore(t.Context(), time.Now().Add(-tc.cutoff))
			if err != nil {
				t.Fatalf("TruncateMessagesBefore: %v", err)
			}
			if removed != tc.wantRemoved {
				t.Errorf("removed = %d; want %d", removed, tc.wantRemoved)
			}

			got, _ := s.GetMessage(t.Context(), retentionTenant, "m1")
			if (got == nil) != (tc.wantRemoved == 1) {
				t.Errorf("message present = %v; want %v", got != nil, tc.wantRemoved == 0)
			}
		})
	}
}

func TestTruncateMessagesBeforeClearsTheRecipientIndex(t *testing.T) {
	s := newRetentionStore(t)
	writeMessage(t, s, message("m1", 48*time.Hour))

	if _, err := s.TruncateMessagesBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("TruncateMessagesBefore: %v", err)
	}

	// The index is what this regressed on before the key construction was
	// shared: the body went and the recipient lookup kept returning a pointer
	// to it, forever, with nothing left to identify the stale entry by.
	for _, recipient := range []string{"to@example.com", "cc@example.com"} {
		got, err := s.GetLatestMessageForRecipient(t.Context(), retentionTenant, recipient)
		if err != nil {
			t.Fatalf("GetLatestMessageForRecipient(%s): %v", recipient, err)
		}
		if got != nil {
			t.Errorf("recipient %s still resolves to message %s", recipient, got.ID)
		}
	}
}

func TestTruncateMessagesBeforeKeepsWhatIsStillWanted(t *testing.T) {
	s := newRetentionStore(t)
	writeMessage(t, s, message("old", 48*time.Hour))
	writeMessage(t, s, message("fresh", time.Hour))

	removed, err := s.TruncateMessagesBefore(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("TruncateMessagesBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}

	if got, _ := s.GetMessage(t.Context(), retentionTenant, "fresh"); got == nil {
		t.Error("pruning an old message took a fresh one with it")
	}
	// The surviving message must still be findable by recipient: both were
	// indexed under the same addresses, so deleting one recipient key too many
	// hides mail that was never expired.
	got, err := s.GetLatestMessageForRecipient(t.Context(), retentionTenant, "to@example.com")
	if err != nil {
		t.Fatalf("GetLatestMessageForRecipient: %v", err)
	}
	if got == nil || got.ID != "fresh" {
		t.Errorf("recipient lookup = %v; want the fresh message", got)
	}
}

func TestTruncateMessagesBeforeSkipsUndatedMessages(t *testing.T) {
	s := newRetentionStore(t)

	undated := message("undated", 0)
	undated.CreatedAt = time.Time{}
	writeMessage(t, s, undated)

	// The zero time precedes every cutoff. Treating it as expired would delete
	// exactly the rows whose age nothing can establish.
	removed, err := s.TruncateMessagesBefore(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("TruncateMessagesBefore: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d; want 0", removed)
	}
	if got, _ := s.GetMessage(t.Context(), retentionTenant, "undated"); got == nil {
		t.Error("deleted a message with no timestamp")
	}
}

func TestTruncateBeforeArchivesAndCountsEvents(t *testing.T) {
	s := newRetentionStore(t)
	inArchiveDir(t)

	event := &entities.EmailEvent{
		ID:        "e1",
		TenantID:  retentionTenant,
		MessageID: "m1",
		Recipient: "to@example.com",
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
		Timestamp: time.Now().Add(-48 * time.Hour),
	}
	if err := s.Write(t.Context(), event); err != nil {
		t.Fatalf("failed to write event: %v", err)
	}
	waitForEvent(t, s, "e1")

	removed, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}

	if got, _ := s.GetByID(t.Context(), retentionTenant, "e1"); got != nil {
		t.Error("event survived its own expiry")
	}
	archives, _, err := s.ListArchives(t.Context(), retentionTenant, 10, "")
	if err != nil {
		t.Fatalf("ListArchives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("archives = %d; want the expired event written to one", len(archives))
	}
}

func waitForEvent(t *testing.T, s stores.EventRepository, id string) {
	t.Helper()

	for range 100 {
		if got, _ := s.GetByID(t.Context(), retentionTenant, id); got != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %s never became readable", id)
}

func TestPruneArchivesBefore(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		age         time.Duration
		wantRemoved int64
		wantGone    bool
	}{
		{
			name:        "an old archive goes",
			filename:    "archive_20260101_000000.jsonl",
			age:         48 * time.Hour,
			wantRemoved: 1,
			wantGone:    true,
		},
		{
			name:     "a recent archive stays",
			filename: "archive_20260817_000000.jsonl",
			age:      time.Hour,
		},
		{
			// The archive directory is a place operators look, and sometimes
			// leave things. Only the files this store writes are ours.
			name:     "a file this store did not write is left alone",
			filename: "notes.txt",
			age:      48 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newRetentionStore(t)
			root := inArchiveDir(t)

			path := filepath.Join(root, retentionTenant, tc.filename)
			writeArchiveFile(t, path, time.Now().Add(-tc.age))

			removed, err := s.PruneArchivesBefore(t.Context(), time.Now().Add(-24*time.Hour))
			if err != nil {
				t.Fatalf("PruneArchivesBefore: %v", err)
			}
			if removed != tc.wantRemoved {
				t.Errorf("removed = %d; want %d", removed, tc.wantRemoved)
			}

			_, err = os.Stat(path)
			if os.IsNotExist(err) != tc.wantGone {
				t.Errorf("%s deleted = %v; want %v", tc.filename, os.IsNotExist(err), tc.wantGone)
			}
		})
	}
}

func TestPruneArchivesBeforeWithNoArchiveDirectory(t *testing.T) {
	s := newRetentionStore(t)
	inArchiveDir(t)

	// A deployment that has never expired an event has no archive directory,
	// which is not an error to report every night.
	removed, err := s.PruneArchivesBefore(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("PruneArchivesBefore: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d; want 0", removed)
	}
}

// inArchiveDir moves the process into a temporary working directory, because
// archiveRoot is relative to it, and returns that root.
func inArchiveDir(t *testing.T) string {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to read the working directory: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to enter the test directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	return archiveRoot
}

func writeArchiveFile(t *testing.T, path string, modTime time.Time) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("failed to create the archive directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("failed to age %s: %v", path, err)
	}
}

// The zero-cutoff guard, on every prune path this store exposes.
//
// These are exported delete methods on the repository interface. A caller that
// passes an unset time — a bug, but a plausible one — would otherwise expire
// the whole store, because subtracting a zero time from MaxInt64 overflows and
// every key compares as older than the result.
func TestPruneRefusesAZeroCutoff(t *testing.T) {
	s := newRetentionStore(t)
	inArchiveDir(t)
	writeMessage(t, s, message("m1", 48*time.Hour))

	prunes := map[string]func() (int64, error){
		"events":   func() (int64, error) { return s.TruncateBefore(t.Context(), time.Time{}) },
		"messages": func() (int64, error) { return s.TruncateMessagesBefore(t.Context(), time.Time{}) },
		"archives": func() (int64, error) { return s.PruneArchivesBefore(t.Context(), time.Time{}) },
	}

	for name, prune := range prunes {
		t.Run(name, func(t *testing.T) {
			removed, err := prune()
			if err == nil {
				t.Fatalf("a zero cutoff was accepted and removed %d records", removed)
			}
			if removed != 0 {
				t.Errorf("removed = %d; want nothing removed before the refusal", removed)
			}
		})
	}

	// The store still has its data.
	if got, _ := s.GetMessage(t.Context(), retentionTenant, "m1"); got == nil {
		t.Error("the message was deleted despite every prune refusing")
	}
}

// Retention scans every tenant's keys in one pass, and nothing in the earlier
// tests exercises more than one tenant. These do.
//
// The property under test is the one the archive code states in its own
// comment: a shared archive file would let anyone who can download an archive
// read every other tenant's mail history. That is a security boundary, and it
// had no coverage.
const otherTenant = "tenant-b"

func eventFor(tenant, id string, age time.Duration) *entities.EmailEvent {
	return &entities.EmailEvent{
		ID:        id,
		TenantID:  tenant,
		MessageID: "msg-" + id,
		Recipient: id + "@example.org",
		Subject:   "subject " + id,
		Type:      panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
		Timestamp: time.Now().Add(-age),
	}
}

func writeEventFor(t *testing.T, s stores.EventRepository, tenant string, e *entities.EmailEvent) {
	t.Helper()

	if err := s.Write(t.Context(), e); err != nil {
		t.Fatalf("write event %s: %v", e.ID, err)
	}
	for range 100 {
		if got, _ := s.GetByID(t.Context(), tenant, e.ID); got != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("event %s never became readable", e.ID)
}

func TestExpiredEventsAreArchivedPerTenant(t *testing.T) {
	s := newRetentionStore(t)
	root := inArchiveDir(t)

	writeEventFor(t, s, retentionTenant, eventFor(retentionTenant, "a-old", 48*time.Hour))
	writeEventFor(t, s, otherTenant, eventFor(otherTenant, "b-old", 48*time.Hour))

	if _, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}

	// Each tenant's history lands under its own directory, and reading one
	// must not reveal the other's recipients or subjects.
	for _, tc := range []struct{ tenant, mine, theirs string }{
		{retentionTenant, "a-old", "b-old"},
		{otherTenant, "b-old", "a-old"},
	} {
		t.Run(tc.tenant, func(t *testing.T) {
			archives, _, err := s.ListArchives(t.Context(), tc.tenant, 10, "")
			if err != nil {
				t.Fatalf("ListArchives: %v", err)
			}
			if len(archives) != 1 {
				t.Fatalf("archives = %d; want 1 for %s", len(archives), tc.tenant)
			}

			body, err := os.ReadFile(filepath.Join(root, tc.tenant, archives[0].Filename))
			if err != nil {
				t.Fatalf("read archive: %v", err)
			}
			if !strings.Contains(string(body), tc.mine) {
				t.Errorf("%s's archive does not contain its own event %s", tc.tenant, tc.mine)
			}
			if strings.Contains(string(body), tc.theirs) {
				t.Errorf("%s's archive contains another tenant's event %s", tc.tenant, tc.theirs)
			}
		})
	}
}

func TestPruningOneTenantLeavesAnotherAlone(t *testing.T) {
	s := newRetentionStore(t)
	inArchiveDir(t)

	// Same cutoff, different ages: retention is a global policy, so what
	// decides survival is age, never which tenant the row belongs to.
	writeEventFor(t, s, retentionTenant, eventFor(retentionTenant, "a-old", 48*time.Hour))
	writeEventFor(t, s, otherTenant, eventFor(otherTenant, "b-fresh", time.Hour))

	writeMessage(t, s, message("a-old-msg", 48*time.Hour))
	fresh := message("b-fresh-msg", time.Hour)
	fresh.TenantID = otherTenant
	fresh.To = []string{"shared@example.org"}
	if err := s.WriteMessage(t.Context(), fresh); err != nil {
		t.Fatalf("write message: %v", err)
	}
	for range 100 {
		if got, _ := s.GetMessage(t.Context(), otherTenant, "b-fresh-msg"); got != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := s.TruncateBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("TruncateBefore: %v", err)
	}
	if _, err := s.TruncateMessagesBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("TruncateMessagesBefore: %v", err)
	}

	if got, _ := s.GetByID(t.Context(), otherTenant, "b-fresh"); got == nil {
		t.Error("a second tenant's fresh event was removed by another tenant's expiry")
	}
	if got, _ := s.GetMessage(t.Context(), otherTenant, "b-fresh-msg"); got == nil {
		t.Error("a second tenant's fresh message body was removed")
	}
	if got, _ := s.GetByID(t.Context(), retentionTenant, "a-old"); got != nil {
		t.Error("the expired event survived")
	}
	if got, _ := s.GetMessage(t.Context(), retentionTenant, "a-old-msg"); got != nil {
		t.Error("the expired message body survived")
	}

	// The recipient index is per tenant. Pruning one tenant's message must not
	// strand or remove the other's lookup.
	got, err := s.GetLatestMessageForRecipient(t.Context(), otherTenant, "shared@example.org")
	if err != nil {
		t.Fatalf("GetLatestMessageForRecipient: %v", err)
	}
	if got == nil || got.ID != "b-fresh-msg" {
		t.Errorf("second tenant's recipient lookup = %v; want its own fresh message", got)
	}
}

// The archive prune walks a directory and deletes from it, which is the shape
// of bug where a symlink turns "tidy up old archives" into "delete something
// else entirely". The behaviour it relies on -- os.ReadDir reporting a symlink
// as not-a-directory, and os.Remove unlinking the symlink rather than its
// target -- is correct but not obvious, so it is pinned here rather than
// assumed by the next person to touch this loop.
func TestPruneArchivesDoesNotFollowSymlinks(t *testing.T) {
	s := newRetentionStore(t)
	root := inArchiveDir(t)

	// Something outside the archive root that must survive.
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious.jsonl")
	writeArchiveFile(t, precious, time.Now().Add(-48*time.Hour))

	tenantDir := filepath.Join(root, retentionTenant)
	if err := os.MkdirAll(tenantDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A symlink that looks exactly like an expired archive.
	link := filepath.Join(tenantDir, "archive_20260101_000000.jsonl")
	if err := os.Symlink(precious, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// And a symlinked directory posing as another tenant.
	linkedDir := filepath.Join(root, "tenant-elsewhere")
	if err := os.Symlink(outside, linkedDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := s.PruneArchivesBefore(t.Context(), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("PruneArchivesBefore: %v", err)
	}

	// The file the symlinks point at is outside the archive root and is not
	// the prune's to touch, however old it is.
	if _, err := os.Stat(precious); err != nil {
		t.Errorf("a file outside the archive root was deleted through a symlink: %v", err)
	}
}
