package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// As a mirror of the shared store, an expiring event is archived here only if
// the shared store does not hold it — so each reaches the archive once.
func TestAMirrorArchivesOnlyWhatIsNotHeldElsewhere(t *testing.T) {
	s := newRetentionStore(t)
	archiveDir := inArchiveDir(t)
	st := s.(*store)

	old := time.Now().Add(-48 * time.Hour)
	for _, id := range []string{"shared", "local-only"} {
		if err := s.Write(t.Context(), &entities.EmailEvent{
			ID: id, TenantID: retentionTenant, MessageID: "m-" + id, Recipient: "r@example.com",
			Type: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, Timestamp: old,
		}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	waitForEvent(t, s, "local-only")
	waitForEvent(t, s, "shared")

	var asked []string
	elsewhere := func(_ context.Context, ids []string) (map[string]bool, error) {
		asked = append(asked, ids...)
		return map[string]bool{"shared": true}, nil
	}
	removed, err := st.TruncateBeforeArchivingOnly(t.Context(), time.Now().Add(-24*time.Hour), elsewhere)
	if err != nil {
		t.Fatalf("TruncateBeforeArchivingOnly: %v", err)
	}
	if removed != 2 || len(asked) != 2 {
		t.Errorf("removed %d, asked about %d; want both events", removed, len(asked))
	}

	archived := readArchive(t, filepath.Join(archiveDir, retentionTenant))
	if len(archived) != 1 || archived[0].ID != "local-only" {
		t.Errorf("archived %v; want only the event the shared store does not hold", archived)
	}
	for _, id := range []string{"shared", "local-only"} {
		if e, _ := s.GetByID(t.Context(), retentionTenant, id); e != nil {
			t.Errorf("%s survived its expiry", id)
		}
	}
}

func readArchive(t *testing.T, dir string) []entities.EmailEvent {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	var out []entities.EmailEvent
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(body), []byte("\n")) {
			var e entities.EmailEvent
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatalf("archive line: %v", err)
			}
			out = append(out, e)
		}
	}
	return out
}
