package worker

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

// The behaviour being bought: a notification survives a failing endpoint, a
// restart, and a burst. The previous worker lost it to all three.

// --- fakes ---

type memDeliveries struct {
	mu    sync.Mutex
	rows  map[string]*entities.WebhookDelivery
	order []string
}

func newMemDeliveries() *memDeliveries {
	return &memDeliveries{rows: map[string]*entities.WebhookDelivery{}}
}

func (m *memDeliveries) Create(_ context.Context, d *entities.WebhookDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := *d
	if copied.NextAttemptAt.IsZero() {
		copied.NextAttemptAt = time.Now()
	}
	m.rows[d.ID] = &copied
	m.order = append(m.order, d.ID)
	return nil
}

func (m *memDeliveries) ClaimDue(_ context.Context, limit int, lease time.Duration) ([]*entities.WebhookDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var due []*entities.WebhookDelivery
	now := time.Now()
	for _, id := range m.order {
		r := m.rows[id]
		if r == nil || len(due) >= limit {
			continue
		}
		terminal := r.Status == entities.DeliveryStatusDelivered || r.Status == entities.DeliveryStatusFailed
		if terminal || r.NextAttemptAt.After(now) {
			continue
		}
		r.Status = entities.DeliveryStatusSending
		copied := *r
		due = append(due, &copied)
	}
	return due, nil
}

func (m *memDeliveries) Update(_ context.Context, d *entities.WebhookDelivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := *d
	m.rows[d.ID] = &copied
	return nil
}

func (m *memDeliveries) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id)
	return nil
}

func (m *memDeliveries) PruneTerminal(context.Context, time.Time) (int64, error) { return 0, nil }

func (m *memDeliveries) get(id string) *entities.WebhookDelivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[id]
}

func (m *memDeliveries) all() []*entities.WebhookDelivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*entities.WebhookDelivery, 0, len(m.rows))
	for _, id := range m.order {
		if r := m.rows[id]; r != nil {
			out = append(out, r)
		}
	}
	return out
}

type fakeSubs struct {
	usecases.WebhookUsecase
	mu   sync.Mutex
	subs []*entities.Webhook
}

func (f *fakeSubs) ListActiveByEvent(context.Context, string, panmailv1.WebhookTriggerEvent) ([]*entities.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*entities.Webhook(nil), f.subs...), nil
}

func (f *fakeSubs) GetSubscription(_ context.Context, _, id string) (*entities.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, nil
}

func (f *fakeSubs) set(subs ...*entities.Webhook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = subs
}

const wTenant = "11111111-1111-1111-1111-111111111111"

func newWorker(deliveries stores.DeliveryRepository, subs usecases.WebhookUsecase) *DurableWorker {
	w := NewDurableWorker(deliveries, subs)
	w.client = &http.Client{Timeout: 2 * time.Second}
	return w
}

// --- tests ---

func TestASuccessfulDeliveryIsMarkedDelivered(t *testing.T) {
	var got *http.Request
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body = make([]byte, r.ContentLength)
		r.Body.Read(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true, Secret: "shhh"})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED, map[string]any{"id": "m1"})
	w.processDue(context.Background())

	rows := deliveries.all()
	if len(rows) != 1 {
		t.Fatalf("queued %d notifications, want 1", len(rows))
	}
	if rows[0].Status != entities.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", rows[0].Status)
	}
	if got == nil || got.Header.Get(SignatureHeader) == "" {
		t.Error("the delivery was not signed, so the receiver cannot tell it from a forgery")
	}
}

// The failure the previous worker lost outright.
func TestAFailingEndpointIsRetriedRatherThanDropped(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	row := deliveries.all()[0]
	if row.Status != entities.DeliveryStatusDeferred {
		t.Fatalf("status = %q, want deferred — a 500 is worth trying again", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Errorf("attempt count = %d, want 1", row.AttemptCount)
	}
	if !row.NextAttemptAt.After(time.Now()) {
		t.Error("no future attempt was scheduled, so the notification is stranded")
	}
	if row.LastError == "" {
		t.Error("nothing recorded about why it failed")
	}
}

// Retrying an unchanged request against an unchanged opinion only keeps
// hitting an endpoint that has already said no.
func TestARejectedRequestIsNotRetried(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	row := deliveries.all()[0]
	if row.Status != entities.DeliveryStatusFailed {
		t.Errorf("status = %q, want failed", row.Status)
	}
}

func TestAnUnreachableEndpointIsRetried(t *testing.T) {
	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	// A port nothing is listening on.
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: "http://127.0.0.1:1/hook", Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	if row := deliveries.all()[0]; row.Status != entities.DeliveryStatusDeferred {
		t.Errorf("status = %q, want deferred — unreachable is the case retrying is for", row.Status)
	}
}

func TestItGivesUpOnceTheScheduleIsExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)

	row := deliveries.all()[0]
	for i := 0; i <= len(retrySchedule); i++ {
		row.NextAttemptAt = time.Now().Add(-time.Second)
		row.Status = entities.DeliveryStatusDeferred
		deliveries.Update(context.Background(), row)
		w.processDue(context.Background())
		row = deliveries.get(row.ID)
	}

	if row.Status != entities.DeliveryStatusFailed {
		t.Errorf("status = %q after %d attempts, want failed — a dead endpoint is not retried forever",
			row.Status, row.AttemptCount)
	}
}

// One row per subscription, so a broken endpoint does not hold up a working
// one beside it.
func TestEachSubscriptionRetriesIndependently(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(
		&entities.Webhook{ID: "good", TenantID: wTenant, URL: good.URL, Active: true},
		&entities.Webhook{ID: "bad", TenantID: wTenant, URL: bad.URL, Active: true},
	)

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	byWebhook := map[string]entities.DeliveryStatus{}
	for _, r := range deliveries.all() {
		byWebhook[r.WebhookID] = r.Status
	}
	if byWebhook["good"] != entities.DeliveryStatusDelivered {
		t.Errorf("the working endpoint got %q", byWebhook["good"])
	}
	if byWebhook["bad"] != entities.DeliveryStatusDeferred {
		t.Errorf("the broken endpoint got %q", byWebhook["bad"])
	}
}

func TestADeletedSubscriptionStopsItsDeliveries(t *testing.T) {
	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: "http://127.0.0.1:1/hook", Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)

	// The tenant removes the subscription before it is delivered.
	subs.set()
	w.processDue(context.Background())

	if row := deliveries.all()[0]; row.Status != entities.DeliveryStatusFailed {
		t.Errorf("status = %q; a notification owed to nobody is finished, not retried forever", row.Status)
	}
}

func TestNoSubscriptionsQueuesNothing(t *testing.T) {
	deliveries := newMemDeliveries()
	w := newWorker(deliveries, &fakeSubs{})

	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)

	if rows := deliveries.all(); len(rows) != 0 {
		t.Errorf("queued %d notifications with nobody subscribed", len(rows))
	}
}

// The previous worker dropped anything past a thousand buffered jobs. A burst
// must queue, not vanish.
func TestABurstIsQueuedRatherThanDropped(t *testing.T) {
	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: "http://127.0.0.1:1/hook", Active: true})

	w := newWorker(deliveries, subs)
	for range 2000 {
		w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	}

	if got := len(deliveries.all()); got != 2000 {
		t.Errorf("queued %d of 2000; the rest were dropped", got)
	}
}

func TestTheDeliveryIdIsStableAcrossRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)

	first := deliveries.all()[0].ID
	row := deliveries.get(first)
	row.NextAttemptAt = time.Now().Add(-time.Second)
	row.Status = entities.DeliveryStatusDeferred
	deliveries.Update(context.Background(), row)
	w.processDue(context.Background())

	// A receiver deduplicates on this; a new id per attempt would defeat that.
	if deliveries.all()[0].ID != first {
		t.Error("the delivery id changed between attempts")
	}
}

// An unreadable subscription is an operational fault, not a deleted one, and
// confusing the two is what made a missed key rotation catastrophic: every
// read fails to decrypt, every queued notification is marked permanently
// failed, and the recorded reason says the subscription was deleted when it
// was not.
type failingSubs struct {
	usecases.WebhookUsecase
	err error
}

func (f *failingSubs) ListActiveByEvent(context.Context, string, panmailv1.WebhookTriggerEvent) ([]*entities.Webhook, error) {
	return []*entities.Webhook{{ID: "sub-1", TenantID: wTenant, URL: "http://127.0.0.1:1/hook", Active: true}}, nil
}

func (f *failingSubs) GetSubscription(context.Context, string, string) (*entities.Webhook, error) {
	return nil, f.err
}

func TestAnUnreadableSubscriptionIsRetriedNotAbandoned(t *testing.T) {
	deliveries := newMemDeliveries()
	subs := &failingSubs{err: errors.New("stored secret could not be decrypted with any configured key")}

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	row := deliveries.all()[0]
	if row.Status != entities.DeliveryStatusDeferred {
		t.Errorf("status = %q, want deferred — restoring the key should recover the queue", row.Status)
	}
	// The reason has to point at the real cause, or an operator goes looking
	// for a subscription that was never deleted.
	if !strings.Contains(row.LastError, "could not be read") {
		t.Errorf("last error = %q, does not say the subscription was unreadable", row.LastError)
	}
}

// A genuinely deleted subscription is still finished rather than retried
// forever.
func TestAGoneSubscriptionIsStillFinished(t *testing.T) {
	deliveries := newMemDeliveries()
	subs := &failingSubs{err: sql.ErrNoRows}

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	if row := deliveries.all()[0]; row.Status != entities.DeliveryStatusFailed {
		t.Errorf("status = %q, want failed for a subscription that is really gone", row.Status)
	}
}

func (m *memDeliveries) Stats(context.Context) (int64, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldest := time.Now()
	for _, r := range m.rows {
		if r.CreatedAt.Before(oldest) {
			oldest = r.CreatedAt
		}
	}
	return int64(len(m.rows)), oldest, nil
}
