package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
)

// importRepo records what reached the store and pretends a chosen set of
// addresses is already suppressed, which is what the conflict clause does in
// the database.
type importRepo struct {
	written  []*entities.Suppression
	existing map[string]bool
}

func (r *importRepo) CreateMany(_ context.Context, sups []*entities.Suppression) (int, error) {
	r.written = append(r.written, sups...)
	inserted := 0
	for _, s := range sups {
		if !r.existing[s.Email] {
			inserted++
		}
	}
	return inserted, nil
}

func (r *importRepo) Create(context.Context, *entities.Suppression) error { return nil }
func (r *importRepo) Delete(context.Context, string, string) error        { return nil }
func (r *importRepo) GetByEmail(context.Context, string, string) (*entities.Suppression, error) {
	return nil, nil
}
func (r *importRepo) GetByEmails(context.Context, string, []string) (map[string]*entities.Suppression, error) {
	return nil, nil
}
func (r *importRepo) List(context.Context, string, int, string) ([]*entities.Suppression, string, error) {
	return nil, "", nil
}

func entries(emails ...string) []*panmailv1.SuppressionEntry {
	out := make([]*panmailv1.SuppressionEntry, 0, len(emails))
	for _, e := range emails {
		out = append(out, &panmailv1.SuppressionEntry{Email: e})
	}
	return out
}

func newImportUsecase() (ManageSuppressionsUsecase, *importRepo) {
	repo := &importRepo{existing: map[string]bool{}}
	return NewManageSuppressionsUsecase(repo), repo
}

func TestImportWritesTheAddressesItCanRead(t *testing.T) {
	uc, repo := newImportUsecase()

	got, err := uc.Import(context.Background(), "tenant-1",
		entries("a@example.com", "b@example.com"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (%+v)", got.Imported, got)
	}
	if len(repo.written) != 2 {
		t.Fatalf("wrote %d rows, want 2", len(repo.written))
	}
	for _, s := range repo.written {
		if s.TenantID != "tenant-1" {
			t.Errorf("tenant = %q, want tenant-1", s.TenantID)
		}
		if s.Reason == "" {
			t.Error("a row was written with no reason")
		}
	}
}

// An imported row stored under a different spelling than the send path looks
// up is a row that never suppresses anything, so the importer has to apply the
// same normalisation Add does.
func TestImportNormalisesTheSameWayAddDoes(t *testing.T) {
	uc, repo := newImportUsecase()

	if _, err := uc.Import(context.Background(), "tenant-1",
		entries("  Alice@Example.COM  ")); err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(repo.written) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(repo.written))
	}
	if want := suppressionKey("Alice@Example.COM"); repo.written[0].Email != want {
		t.Errorf("stored %q, want %q", repo.written[0].Email, want)
	}
}

// Exported lists often carry the display-name form, and refusing those would
// reject most of a real file.
func TestImportAcceptsTheDisplayNameForm(t *testing.T) {
	uc, repo := newImportUsecase()

	got, err := uc.Import(context.Background(), "tenant-1",
		entries("Alice Smith <alice@example.com>"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got.Invalid != 0 {
		t.Fatalf("invalid = %d, want 0 (%+v)", got.Invalid, got)
	}
	if repo.written[0].Email != "alice@example.com" {
		t.Errorf("stored %q, want the bare address", repo.written[0].Email)
	}
}

// ON CONFLICT does not see a duplicate inside its own VALUES list; PostgreSQL
// fails the whole statement with a cardinality violation. A file that lists an
// address twice is ordinary, so the duplicate is dropped before the write.
func TestImportDeduplicatesWithinTheFile(t *testing.T) {
	uc, repo := newImportUsecase()

	got, err := uc.Import(context.Background(), "tenant-1",
		entries("dup@example.com", "DUP@example.com", "other@example.com"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(repo.written) != 2 {
		t.Fatalf("wrote %d rows, want 2 — the repeat must not reach the statement", len(repo.written))
	}
	if got.AlreadySuppressed != 1 {
		t.Errorf("alreadySuppressed = %d, want 1", got.AlreadySuppressed)
	}
}

func TestImportCountsWhatWasAlreadyStored(t *testing.T) {
	uc, repo := newImportUsecase()
	repo.existing["known@example.com"] = true

	got, err := uc.Import(context.Background(), "tenant-1",
		entries("known@example.com", "fresh@example.com"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got.Imported != 1 || got.AlreadySuppressed != 1 {
		t.Fatalf("got %+v, want 1 imported and 1 already suppressed", got)
	}
}

// One bad line must not cost the rest of the file.
func TestImportSkipsWhatItCannotRead(t *testing.T) {
	uc, repo := newImportUsecase()

	got, err := uc.Import(context.Background(), "tenant-1",
		entries("good@example.com", "not an address", "", "also-good@example.com"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got.Imported != 2 {
		t.Errorf("imported = %d, want 2 (%+v)", got.Imported, got)
	}
	if got.Invalid != 2 {
		t.Errorf("invalid = %d, want 2 (%+v)", got.Invalid, got)
	}
	if len(repo.written) != 2 {
		t.Errorf("wrote %d rows, want 2", len(repo.written))
	}
}

// The counts are how a caller tells "nothing to do" from "nothing worked", so
// they have to account for every entry submitted.
func TestImportCountsAccountForEveryEntry(t *testing.T) {
	uc, repo := newImportUsecase()
	repo.existing["known@example.com"] = true

	submitted := entries(
		"fresh@example.com",
		"known@example.com",
		"repeat@example.com", "repeat@example.com",
		"nonsense",
	)

	got, err := uc.Import(context.Background(), "tenant-1", submitted)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	total := got.Imported + got.AlreadySuppressed + got.Invalid
	if total != len(submitted) {
		t.Fatalf("counts sum to %d, want %d (%+v)", total, len(submitted), got)
	}
}

// Enough to see the shape of the mistake, not enough to echo a bad file back.
func TestImportBoundsTheRejectedSample(t *testing.T) {
	uc, _ := newImportUsecase()

	bad := make([]string, 0, maxInvalidSamples*3)
	for i := range cap(bad) {
		bad = append(bad, fmt.Sprintf("not an address %d", i))
	}

	got, err := uc.Import(context.Background(), "tenant-1", entries(bad...))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if got.Invalid != len(bad) {
		t.Errorf("invalid = %d, want %d", got.Invalid, len(bad))
	}
	if len(got.InvalidSamples) != maxInvalidSamples {
		t.Errorf("samples = %d, want them capped at %d", len(got.InvalidSamples), maxInvalidSamples)
	}
}

// The list is held in memory while it is parsed, so it needs a ceiling for the
// same reason a webhook body does.
func TestImportRefusesAnOversizedBatch(t *testing.T) {
	uc, repo := newImportUsecase()

	emails := make([]string, MaxImportEntries+1)
	for i := range emails {
		emails[i] = fmt.Sprintf("user%d@example.com", i)
	}

	if _, err := uc.Import(context.Background(), "tenant-1", entries(emails...)); err == nil {
		t.Fatal("an oversized batch was accepted")
	}
	if len(repo.written) != 0 {
		t.Errorf("wrote %d rows for a refused batch, want 0", len(repo.written))
	}
}

// A reason from the source list is kept; a blank one gets wording that says
// where the entry came from, so the suppressions page never shows a bare row.
func TestImportKeepsAGivenReasonAndSuppliesADefault(t *testing.T) {
	uc, repo := newImportUsecase()

	_, err := uc.Import(context.Background(), "tenant-1", []*panmailv1.SuppressionEntry{
		{Email: "given@example.com", Reason: "hard bounce at SendGrid"},
		{Email: "blank@example.com"},
	})
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	byEmail := map[string]string{}
	for _, s := range repo.written {
		byEmail[s.Email] = s.Reason
	}
	if byEmail["given@example.com"] != "hard bounce at SendGrid" {
		t.Errorf("reason = %q, want the one from the file", byEmail["given@example.com"])
	}
	if !strings.Contains(strings.ToLower(byEmail["blank@example.com"]), "import") {
		t.Errorf("default reason = %q, want it to say the entry was imported", byEmail["blank@example.com"])
	}
}

// An empty address used to be stored: one row per tenant with nothing in it,
// occupying the UNIQUE(tenant_id, email) slot, matching no send, and reported
// as success. Add and Remove now refuse it, and Check answers false without a
// query.
//
// This test was first written, and named, on the belief that gsmail's
// NormalizeAddress spun forever on an empty input. It does not -- that was a
// goroutine dump misread on a saturated machine; see the correction on #57.
// The deadlines cost nothing, so they stay: a regression still fails the suite
// rather than hanging it.
func TestAnEmptyAddressIsRefused(t *testing.T) {
	within := func(t *testing.T, name string, fn func()) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			fn()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not return on an empty address", name)
		}
	}

	uc, repo := newImportUsecase()
	ctx := context.Background()

	for _, blank := range []string{"", "   ", "\t"} {
		within(t, "Add", func() {
			if _, err := uc.Add(ctx, "tenant-1", &panmailv1.AddSuppressionRequest{Email: blank}); !errors.Is(err, ErrEmptyAddress) {
				t.Errorf("Add(%q) error = %v, want ErrEmptyAddress", blank, err)
			}
		})

		within(t, "Remove", func() {
			if err := uc.Remove(ctx, "tenant-1", blank); !errors.Is(err, ErrEmptyAddress) {
				t.Errorf("Remove(%q) error = %v, want ErrEmptyAddress", blank, err)
			}
		})

		within(t, "Check", func() {
			suppressed, _, err := uc.Check(ctx, "tenant-1", blank)
			if err != nil || suppressed {
				t.Errorf("Check(%q) = %v, %v; want false, nil", blank, suppressed, err)
			}
		})

		within(t, "Import", func() {
			got, err := uc.Import(ctx, "tenant-1", entries(blank))
			if err != nil {
				t.Errorf("Import(%q) error = %v", blank, err)
			}
			if got.Invalid != 1 {
				t.Errorf("Import(%q) invalid = %d, want 1", blank, got.Invalid)
			}
		})
	}

	// Nothing unmatchable reached the store: a row with an empty address
	// would sit on the list matching nothing and blocking the one unique slot.
	if len(repo.written) != 0 {
		t.Errorf("wrote %d rows for blank addresses, want 0", len(repo.written))
	}
}
