package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores/postgres"
)

// A slow endpoint that owns more than a batch's worth of the oldest due
// notifications used to take every batch: the claim was oldest-first and
// nothing else, so another subscription's notification waited until the slow
// one's backlog had been worked through -- at the client timeout per row.
//
// Run against the real store, because the fairness lives in the claim query.
func TestASlowEndpointOwningTheOldestRowsDoesNotStarveAnother(t *testing.T) {
	var slowHits atomic.Int32
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowHits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)

	var fastHits atomic.Int32
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fastHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer fast.Close()

	const slowSub = "44444444-4444-4444-4444-444444444444"
	const fastSub = "55555555-5555-5555-5555-555555555555"
	deliveries := postgres.NewDeliveryStore(storetest.NewConnection(t))
	subs := &fakeSubs{}

	w := NewDurableWorker(deliveries, subs)
	w.client.Timeout = 200 * time.Millisecond
	w.batchSize = 10
	w.perSubscription = 4

	// Three batches' worth for the slow endpoint, all older than the one
	// notification the working endpoint is owed.
	subs.set(&entities.Webhook{ID: slowSub, TenantID: wTenant, URL: slow.URL, Active: true})
	for range 30 {
		w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	}
	// Enqueue fans out to every listed subscription, so each is listed alone
	// while its notifications are queued.
	subs.set(&entities.Webhook{ID: fastSub, TenantID: storetest.TenantB, URL: fast.URL, Active: true})
	w.Enqueue(storetest.TenantB, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	subs.set(
		&entities.Webhook{ID: slowSub, TenantID: wTenant, URL: slow.URL, Active: true},
		&entities.Webhook{ID: fastSub, TenantID: storetest.TenantB, URL: fast.URL, Active: true},
	)

	w.processDue(context.Background())

	if fastHits.Load() != 1 {
		t.Fatalf("the working endpoint got %d deliveries from the first batch; it waited behind the slow one's backlog", fastHits.Load())
	}
	// Two timeouts in a row, then the rest of its share went back rather
	// than holding a worker for the client timeout each.
	if got := slowHits.Load(); got != int32(w.failureStreak) {
		t.Errorf("the slow endpoint was tried %d times in one batch, want %d before backing off", got, w.failureStreak)
	}

	// And it stays backed off: the next batch does not hand it more workers.
	w.processDue(context.Background())
	if got := slowHits.Load(); got != int32(w.failureStreak) {
		t.Errorf("the slow endpoint was tried again (%d attempts) inside its back-off", got)
	}
}
