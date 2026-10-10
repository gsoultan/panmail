package pebble

import (
	"strings"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// The provider is recorded once, never overwritten, and the body around it —
// attachments included — survives the rewrite intact.
func TestBackfillMessageProviderSetsItOnce(t *testing.T) {
	s := newRetentionStore(t)
	st := s.(*store)

	m := message("m1", 0)
	m.Attachments = []*panmailv1.Attachment{{Filename: "big.bin", Content: []byte(strings.Repeat("x", 1<<20))}}
	writeMessage(t, s, m)

	if err := st.BackfillMessageProvider(t.Context(), retentionTenant, "m1", "prov-1"); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	waitFor(t, func() bool {
		got, _ := s.GetMessage(t.Context(), retentionTenant, "m1")
		return got != nil && got.ProviderID == "prov-1"
	})

	if err := st.BackfillMessageProvider(t.Context(), retentionTenant, "m1", "prov-2"); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	got, _ := s.GetMessage(t.Context(), retentionTenant, "m1")
	if got.ProviderID != "prov-1" {
		t.Errorf("provider = %q; a later delivery overwrote the first", got.ProviderID)
	}
	if len(got.Attachments) != 1 || len(got.Attachments[0].Content) != 1<<20 {
		t.Error("the attachment did not survive the backfill")
	}

	// A message that is not there is not an error: there is nothing to
	// record the provider on.
	if err := st.BackfillMessageProvider(t.Context(), retentionTenant, "absent", "prov-1"); err != nil {
		t.Errorf("backfill of a missing message: %v", err)
	}
}

func TestStoredProviderIDReadsOnlyThatField(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"set", `{"id":"m1","tenant_id":"t","provider_id":"p1","body_html":"<p>"}`, "p1"},
		{"empty", `{"id":"m1","tenant_id":"t","provider_id":"","body_html":"<p>"}`, ""},
		{"absent", `{"id":"m1","tenant_id":"t"}`, ""},
		{"later in the object", `{"attachments":[{"content":"eA=="}],"provider_id":"p2"}`, "p2"},
		// Stops at the field: what follows is never parsed, so neither its
		// size nor even its validity matters.
		{"stops at the field", `{"provider_id":"p3","body_html":` + strings.Repeat("{", 64), "p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := storedProviderID([]byte(tt.json))
			if err != nil {
				t.Fatalf("storedProviderID: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q; want %q", got, tt.want)
			}
		})
	}

	if _, err := storedProviderID([]byte(`["not an object"]`)); err == nil {
		t.Error("a value that is not an object was read as one")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never held")
}
