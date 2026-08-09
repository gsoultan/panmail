package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/ratelimit"
	tenantEntities "github.com/gsoultan/panmail/internal/tenant/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

const rateTenant = "tenant-1"

type stubLimits struct {
	limit ratelimit.Limit
	err   error
	calls int
}

func (s *stubLimits) SendLimitFor(context.Context, string) (ratelimit.Limit, error) {
	s.calls++
	return s.limit, s.err
}

func usecaseWithLimit(limits SendLimits) *sendEmailUsecase {
	return &sendEmailUsecase{limiter: ratelimit.New(), sendLimits: limits}
}

func TestRecipientCountChargesDeliveriesNotRequests(t *testing.T) {
	// One call addressed to fifty people is fifty messages handed to a
	// provider. Charging per request would let a single call carry unlimited
	// volume, which is the exact shape of the runaway send this prevents.
	if got := recipientCount([]string{"a@x", "b@x"}, []string{"c@x"}, []string{"d@x"}); got != 4 {
		t.Errorf("cost = %d, want 4 across to, cc and bcc", got)
	}
	// Never free: a send with no parsed recipients still costs something, or a
	// malformed loop would be unlimited.
	if got := recipientCount(nil, nil, nil); got != 1 {
		t.Errorf("cost = %d, want 1 for an empty recipient list", got)
	}
}

func TestATenantWithNoLimitIsNotChecked(t *testing.T) {
	limits := &stubLimits{limit: ratelimit.Limit{}}
	u := usecaseWithLimit(limits)

	for i := range 100 {
		if err := u.checkSendRate(context.Background(), rateTenant, 10); err != nil {
			t.Fatalf("send %d refused for an unlimited tenant: %v", i, err)
		}
	}
}

func TestSendingOverTheRateIsRefusedWithARetryTime(t *testing.T) {
	limits := &stubLimits{limit: ratelimit.Limit{PerMinute: 60, Burst: 5}}
	u := usecaseWithLimit(limits)

	for i := range 5 {
		if err := u.checkSendRate(context.Background(), rateTenant, 1); err != nil {
			t.Fatalf("send %d refused inside the burst: %v", i+1, err)
		}
	}

	err := u.checkSendRate(context.Background(), rateTenant, 1)
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("error = %v, want a RateLimitedError so callers can tell it apart", err)
	}
	if limited.TenantID != rateTenant {
		t.Errorf("tenant = %q, want %q", limited.TenantID, rateTenant)
	}
	if limited.RetryAfter <= 0 {
		t.Error("a refusal must say when to come back, or a client can only guess and will guess badly")
	}
}

func TestOneLargeSendCanExhaustTheAllowance(t *testing.T) {
	limits := &stubLimits{limit: ratelimit.Limit{PerMinute: 600, Burst: 10}}
	u := usecaseWithLimit(limits)

	// Eight recipients in one call, so only two remain.
	if err := u.checkSendRate(context.Background(), rateTenant, 8); err != nil {
		t.Fatalf("eight recipients should fit a burst of ten: %v", err)
	}
	if err := u.checkSendRate(context.Background(), rateTenant, 5); err == nil {
		t.Error("five more should not fit in the two that remain")
	}
}

// The limiter protects a shared sending reputation. A database hiccup that
// silently stopped all outbound mail would be a far worse outage than briefly
// missing a ceiling, so an unreadable limit lets the send through.
func TestAnUnreadableLimitFailsOpen(t *testing.T) {
	limits := &stubLimits{err: errors.New("database is unreachable")}
	u := usecaseWithLimit(limits)

	for range 100 {
		if err := u.checkSendRate(context.Background(), rateTenant, 1); err != nil {
			t.Fatalf("a send was refused because the limit could not be read: %v", err)
		}
	}
}

// Without a limiter configured the path behaves exactly as it did before the
// ceiling existed.
func TestNoLimiterMeansNoLimiting(t *testing.T) {
	u := &sendEmailUsecase{}
	for range 100 {
		if err := u.checkSendRate(context.Background(), rateTenant, 100); err != nil {
			t.Fatalf("unconfigured limiter refused a send: %v", err)
		}
	}
}

func TestTenantsAreChargedSeparately(t *testing.T) {
	limits := &stubLimits{limit: ratelimit.Limit{PerMinute: 60, Burst: 3}}
	u := usecaseWithLimit(limits)

	for range 3 {
		u.checkSendRate(context.Background(), "noisy", 1)
	}
	if err := u.checkSendRate(context.Background(), "noisy", 1); err == nil {
		t.Fatal("the noisy tenant should be capped")
	}
	if err := u.checkSendRate(context.Background(), "quiet", 1); err != nil {
		t.Errorf("a second tenant was refused because of the first: %v", err)
	}
}

// --- the tenant lookup ---

type stubTenants struct {
	tenant *tenantEntities.Tenant
	calls  int
}

func (s *stubTenants) GetTenantByID(context.Context, string) (*tenantEntities.Tenant, error) {
	s.calls++
	return s.tenant, nil
}

func TestTheTenantLimitIsCachedOffTheHotPath(t *testing.T) {
	tenants := &stubTenants{tenant: &tenantEntities.Tenant{SendRatePerMinute: 100, SendBurst: 20}}
	limits := NewTenantSendLimits(tenants)

	for range 50 {
		limit, err := limits.SendLimitFor(context.Background(), rateTenant)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if limit.PerMinute != 100 || limit.Burst != 20 {
			t.Fatalf("limit = %+v, want the tenant's configured values", limit)
		}
	}

	// Reading it per message would put a database round trip on the one
	// operation that most needs to stay cheap, multiplied across a queue drain.
	if tenants.calls != 1 {
		t.Errorf("read the tenant %d times for 50 sends, want 1", tenants.calls)
	}
}

func TestAMissingTenantIsTreatedAsUnlimited(t *testing.T) {
	limits := NewTenantSendLimits(&stubTenants{tenant: nil})

	limit, err := limits.SendLimitFor(context.Background(), rateTenant)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	// The request is already authenticated; a missing record is not a reason
	// to stop mail.
	if !limit.Unlimited() {
		t.Errorf("limit = %+v, want unlimited", limit)
	}
}

// --- the worker ---

// The worker's failure accounting is unchanged by the limiter, which now sits
// at admission rather than on this path.
func TestTheWorkerStillCountsRealFailures(t *testing.T) {
	outboxRepo := &workerMockOutboxRepo{}
	emailUsecase := &mockEmailUsecase{err: errors.New("421 4.3.0 Temporary failure")}

	w := NewQueueWorker(outboxRepo, emailUsecase, &mockSuppressionUsecase{}, &mockTenantUsecase{}, time.Second).(*queueWorker)

	req := panmailv1.SendEmailRequest{
		To: []string{"test@example.com"}, From: "sender@example.com", Subject: "Test", Body: "Hello",
	}
	reqBytes, _ := protojson.Marshal(&req)

	email := &entities.OutboxEmail{
		ID: "124", TenantID: rateTenant, Request: reqBytes,
		Status: entities.OutboxStatusPending, RetryCount: 0, NextRetryAt: time.Now(),
	}

	w.processEmail(context.Background(), email)

	if outboxRepo.lastUpdate.RetryCount != 1 {
		t.Errorf("retry count = %d, want 1 for a real delivery failure", outboxRepo.lastUpdate.RetryCount)
	}
}
