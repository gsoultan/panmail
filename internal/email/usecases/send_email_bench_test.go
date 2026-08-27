package usecases

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	suppressionentities "github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// dbRoundTrip is what one query costs against a database on another host. The
// counting repositories below sleep for it so a benchmark measures the thing
// that actually dominates admission — round trips — rather than the cost of a
// map lookup in a fake.
//
// Kept small so the suite stays quick; the ratio between recipient counts is
// what matters, not the absolute number.
const dbRoundTrip = 200 * time.Microsecond

// quietLogs silences the send path's structured logging for the duration of a
// benchmark. Admission logs one line per message, and at these iteration counts
// the writes to stderr cost more than the code being measured.
func quietLogs(b *testing.B) {
	b.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })
}

// countingSuppressionRepo records how many lookups a send costs.
type countingSuppressionRepo struct {
	calls   atomic.Int64
	latency time.Duration
}

func (m *countingSuppressionRepo) Create(context.Context, *suppressionentities.Suppression) error {
	return nil
}

func (m *countingSuppressionRepo) Delete(context.Context, string, string) error { return nil }

func (m *countingSuppressionRepo) GetByEmail(
	_ context.Context, _, _ string,
) (*suppressionentities.Suppression, error) {
	m.calls.Add(1)
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	return nil, nil
}

// GetByEmails is one round trip regardless of how many addresses it carries,
// which is the whole point of it existing.
func (m *countingSuppressionRepo) GetByEmails(
	_ context.Context, _ string, _ []string,
) (map[string]*suppressionentities.Suppression, error) {
	m.calls.Add(1)
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	return map[string]*suppressionentities.Suppression{}, nil
}

func (m *countingSuppressionRepo) List(
	context.Context, string, int, string,
) ([]*suppressionentities.Suppression, string, error) {
	return nil, "", nil
}

func benchRecipients(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("recipient-%d@example.com", i)
	}
	return out
}

func benchUsecase(suppressionRepo *countingSuppressionRepo) SendEmailUsecase {
	return NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: suppressionRepo,
		OutboxRepo:      &mockOutboxRepo{},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: &mockSender{}},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	})
}

func benchRequest(recipients []string) *panmailv1.SendEmailRequest {
	return &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         recipients,
		Subject:    "Hello",
		BodyText:   "This is a test message.",
	}
}

// BenchmarkSendEmailAdmission measures the admission path as the recipient
// count grows. Admission is the request a caller waits on, so its cost per
// recipient is what a large send feels like.
func BenchmarkSendEmailAdmission(b *testing.B) {
	for _, recipients := range []int{1, 10, 50, 100} {
		b.Run(fmt.Sprintf("recipients=%d", recipients), func(b *testing.B) {
			quietLogs(b)
			repo := &countingSuppressionRepo{latency: dbRoundTrip}
			usecase := benchUsecase(repo)
			req := benchRequest(benchRecipients(recipients))
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := usecase.SendEmail(ctx, testTenantID, req); err != nil {
					b.Fatalf("SendEmail: %v", err)
				}
			}
			b.StopTimer()

			b.ReportMetric(float64(repo.calls.Load())/float64(b.N), "db-round-trips/op")
		})
	}
}

// BenchmarkSendEmailAdmissionCPU measures the same path with no simulated
// round trip, so a CPU profile taken over it shows the work admission actually
// does rather than the time it spends waiting for a database.
//
// Run it with:
//
//	go test -run '^$' -bench AdmissionCPU -cpuprofile cpu.prof ./internal/email/usecases/
func BenchmarkSendEmailAdmissionCPU(b *testing.B) {
	for _, recipients := range []int{1, 100} {
		b.Run(fmt.Sprintf("recipients=%d", recipients), func(b *testing.B) {
			quietLogs(b)
			usecase := benchUsecase(&countingSuppressionRepo{})
			req := benchRequest(benchRecipients(recipients))
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := usecase.SendEmail(ctx, testTenantID, req); err != nil {
					b.Fatalf("SendEmail: %v", err)
				}
			}
		})
	}
}

// TestAdmissionStaysWithinItsAllocationBudget is the guard on the work above.
//
// Allocation was the largest CPU cost on this path — parsing addresses alone
// was 76% of it — and the two fixes that brought it down, parsing each address
// once and looking suppressions up in one query, are both the kind of thing a
// later change reintroduces without noticing. A benchmark would not catch that
// because nothing runs benchmarks; this runs with the suite.
//
// The budget is deliberately loose. It exists to catch a change that puts the
// per-recipient work back, which would roughly double the count, not to police
// a few allocations either way.
func TestAdmissionStaysWithinItsAllocationBudget(t *testing.T) {
	testCases := []struct {
		name       string
		recipients int
		budget     float64
	}{
		// 40 today.
		{name: "one recipient", recipients: 1, budget: 60},
		// 741 today. Reintroducing a parse or a query per recipient would put
		// this well past the budget.
		{name: "a hundred recipients", recipients: 100, budget: 1000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			usecase := benchUsecase(&countingSuppressionRepo{})
			req := benchRequest(benchRecipients(tc.recipients))
			ctx := context.Background()

			got := testing.AllocsPerRun(50, func() {
				if _, err := usecase.SendEmail(ctx, testTenantID, req); err != nil {
					t.Fatalf("SendEmail() error = %v", err)
				}
			})

			if got > tc.budget {
				t.Errorf(
					"admission allocated %.0f times for %d recipients, over the budget of %.0f",
					got, tc.recipients, tc.budget,
				)
			}
		})
	}
}
