package usecases

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	outboxstore "github.com/gsoultan/panmail/internal/email/repositories/stores/postgres"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/storetest"
	suppressionentities "github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// The idempotency tests run against the real outbox store. Whether two sends
// with one key queue once is decided by the database's unique constraint and
// the transaction around it, and a stub would only assert what the stub does.

// lockedEvents is mockEventUsecase made safe for concurrent sends.
type lockedEvents struct {
	mockEventUsecase
	mu sync.Mutex
}

func (m *lockedEvents) RecordEvent(ctx context.Context, tenantID, providerID, messageID string, eventType panmailv1.EmailEventType, recipient string, subject string, errorMessage string, metadata map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mockEventUsecase.RecordEvent(ctx, tenantID, providerID, messageID, eventType, recipient, subject, errorMessage, metadata)
}

func (m *lockedEvents) SaveMessage(ctx context.Context, message *panmailv1.EmailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mockEventUsecase.SaveMessage(ctx, message)
}

// switchableSuppression suppresses everyone until lifted.
type switchableSuppression struct {
	mockSuppressionRepo
	mu     sync.Mutex
	active bool
}

func (s *switchableSuppression) GetByEmails(_ context.Context, _ string, emails []string) (map[string]*suppressionentities.Suppression, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := map[string]*suppressionentities.Suppression{}
	if s.active {
		for _, e := range emails {
			found[strings.ToLower(e)] = &suppressionentities.Suppression{Email: e, Reason: "complaint"}
		}
	}
	return found, nil
}

type keyHarness struct {
	send   *sendEmailUsecase
	sqlDB  *sql.DB
	events *lockedEvents
	clock  time.Time
}

func newKeyHarness(t *testing.T, suppression *switchableSuppression) *keyHarness {
	t.Helper()
	sqlDB := storetest.NewDB(t)
	events := &lockedEvents{}
	deps := SendEmailDeps{
		ProviderRepo: &mockProviderRepo{provider: &providerEntities.EmailProvider{
			ID: testProviderID, TenantID: storetest.TenantA, Name: "test", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		}},
		TemplateRepo:    &mockTemplateRepo{},
		SuppressionRepo: &mockSuppressionRepo{},
		OutboxRepo:      outboxstore.NewOutboxStore(db.NewConnection(sqlDB)),
		EventUsecase:    events,
		ProviderFactory: &mockFactory{sender: &mockSender{}},
		Renderer:        NewTemplateRenderer(),
		BaseURL:         "http://localhost",
		TrackingSigner:  tracking.NewSigner([]byte("test-tracking-key")),
	}
	if suppression != nil {
		deps.SuppressionRepo = suppression
	}
	h := &keyHarness{sqlDB: sqlDB, events: events, clock: time.Now()}
	h.send = NewSendEmailUsecase(deps).(*sendEmailUsecase)
	h.send.now = func() time.Time { return h.clock }
	return h
}

func keyedRequest(key, subject string) *panmailv1.SendEmailRequest {
	return &panmailv1.SendEmailRequest{
		ProviderId: testProviderID, From: "from@example.com",
		To: []string{"to@example.com"}, Subject: subject, BodyText: "hi",
		IdempotencyKey: key,
	}
}

func (h *keyHarness) queued(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.sqlDB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE tenant_id = $1`, storetest.TenantA).Scan(&n); err != nil {
		t.Fatalf("counting outbox rows: %v", err)
	}
	return n
}

func TestARepeatedKeyReturnsTheFirstMessageAndQueuesOnce(t *testing.T) {
	h := newKeyHarness(t, nil)
	ctx := context.Background()

	first, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("repeat send: %v", err)
	}

	if second.MessageId != first.MessageId || second.Status != first.Status {
		t.Errorf("repeat answered %s/%s; want the first response %s/%s",
			second.MessageId, second.Status, first.MessageId, first.Status)
	}
	if n := h.queued(t); n != 1 {
		t.Errorf("%d messages queued; want 1", n)
	}
	if n := len(h.events.messages); n != 1 {
		t.Errorf("%d messages saved for analytics; want 1", n)
	}

	// The key is the tenant's own. Another tenant using the same string is
	// sending its own message.
	if _, err := h.send.SendEmail(ctx, storetest.TenantB, keyedRequest("order-42", "Receipt")); err != nil {
		t.Fatalf("other tenant's send: %v", err)
	}
	var other int
	if err := h.sqlDB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE tenant_id = $1`, storetest.TenantB).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if other != 1 {
		t.Errorf("tenant B's send with tenant A's key queued %d messages; want 1", other)
	}
}

func TestAKeyReusedForADifferentRequestIsRefused(t *testing.T) {
	h := newKeyHarness(t, nil)
	ctx := context.Background()

	first, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	_, err = h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "A different receipt"))

	var conflict *IdempotencyKeyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v; want IdempotencyKeyConflictError", err)
	}
	if conflict.MessageID != first.MessageId {
		t.Errorf("conflict names %s; want the first message %s", conflict.MessageID, first.MessageId)
	}
	if n := h.queued(t); n != 1 {
		t.Errorf("%d messages queued; want 1", n)
	}
}

func TestConcurrentSendsWithOneKeyQueueOnce(t *testing.T) {
	h := newKeyHarness(t, nil)
	ctx := context.Background()

	const senders = 8
	ids := make([]string, senders)
	errs := make([]error, senders)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range senders {
		wg.Go(func() {
			<-start
			res, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
			errs[i] = err
			if res != nil {
				ids[i] = res.MessageId
			}
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("sender %d: %v", i, err)
		}
		if ids[i] != ids[0] {
			t.Errorf("sender %d was told %s, sender 0 %s; every one should get the winner's message", i, ids[i], ids[0])
		}
	}
	if n := h.queued(t); n != 1 {
		t.Errorf("%d messages queued; want 1", n)
	}
	if n := len(h.events.messages); n != 1 {
		t.Errorf("%d messages saved for analytics; want 1, the losers' must leave nothing", n)
	}
}

func TestAKeyExpiresAfterItsWindow(t *testing.T) {
	h := newKeyHarness(t, nil)
	ctx := context.Background()

	h.clock = time.Now().Add(-IdempotencyKeyWindow - time.Minute)
	first, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	// A different request, too: past the window the key answers nothing, so
	// it neither replays nor conflicts.
	h.clock = time.Now()
	second, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Next month's receipt"))
	if err != nil {
		t.Fatalf("send after the window: %v", err)
	}
	if second.MessageId == first.MessageId {
		t.Error("a send after the window was answered with the expired key's message")
	}
	if n := h.queued(t); n != 2 {
		t.Errorf("%d messages queued; want 2", n)
	}

	// And the reused key is now the new message's.
	third, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Next month's receipt"))
	if err != nil || third.MessageId != second.MessageId {
		t.Errorf("repeat of the second send = %v, %v; want %s", third, err, second.MessageId)
	}
}

// A refusal stores nothing. A retry after one is decided afresh, so a caller
// is not held to a rate limit or suppression that has since cleared.
func TestARefusedSendDoesNotSpendItsKey(t *testing.T) {
	suppression := &switchableSuppression{active: true}
	h := newKeyHarness(t, suppression)
	ctx := context.Background()

	_, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	var suppressed *SuppressedRecipientError
	if !errors.As(err, &suppressed) {
		t.Fatalf("err = %v; want the suppression refusal", err)
	}

	suppression.mu.Lock()
	suppression.active = false
	suppression.mu.Unlock()

	res, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("retry after the suppression was lifted: %v", err)
	}
	if res.MessageId == "" {
		t.Fatal("retry was accepted without a message id")
	}
	if n := h.queued(t); n != 1 {
		t.Errorf("%d messages queued; want 1", n)
	}
}

func TestAnUnusableKeyIsRefused(t *testing.T) {
	h := newKeyHarness(t, nil)
	for name, key := range map[string]string{
		"too long":        strings.Repeat("k", MaxIdempotencyKeyLength+1),
		"control":         "order\n42",
		"non-ascii":       "commande-é",
		"empty is no key": "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.send.SendEmail(context.Background(), storetest.TenantA, keyedRequest(key, "Receipt"))
			var bad *InvalidIdempotencyKeyError
			if key == "" {
				if err != nil {
					t.Fatalf("no key: %v", err)
				}
				return
			}
			if !errors.As(err, &bad) {
				t.Errorf("err = %v; want InvalidIdempotencyKeyError", err)
			}
		})
	}
	if err := ValidateIdempotencyKey(strings.Repeat("k", MaxIdempotencyKeyLength-6) + " ~!@#$"); err != nil {
		t.Errorf("a %d-character printable key was refused: %v", MaxIdempotencyKeyLength, err)
	}
}

// The worker's delivery pass carries the key in the stored request. It must
// deliver, not find the key already used and answer with the acceptance.
func TestTheWorkerPassIgnoresTheKey(t *testing.T) {
	h := newKeyHarness(t, nil)
	ctx := context.Background()

	res, err := h.send.SendEmail(ctx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	workerCtx := context.WithValue(context.WithValue(ctx, SkipOutboxKey, true), MessageIDKey, res.MessageId)
	delivered, err := h.send.SendEmail(workerCtx, storetest.TenantA, keyedRequest("order-42", "Receipt"))
	if err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if delivered.Status != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED {
		t.Errorf("worker pass answered %s; want DELIVERED", delivered.Status)
	}
}
