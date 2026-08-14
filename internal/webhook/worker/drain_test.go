package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

// A delivery already in flight when the gateway is signalled has to finish.
//
// Cancelling it mid-request is not a clean stop: the endpoint may have received
// and acted on the notification, and panmail cannot tell, so it records a
// failure and delivers again. The receiver sees the event twice. Signatures let
// them deduplicate; handing them that problem on every deploy is not a design.

func TestADeliveryInFlightIsNotCutOffByShutdown(t *testing.T) {
	var completed atomic.Bool
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold the request open until the test has signalled shutdown, so the
		// cancellation lands squarely in the middle of it.
		select {
		case <-release:
		case <-r.Context().Done():
			// The failure this exists for: the request was cancelled from
			// panmail's side while the endpoint was still working.
			return
		case <-time.After(5 * time.Second):
		}
		completed.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true, Secret: "shhh"})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED, map[string]any{"id": "m1"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.processDue(ctx)
	}()

	// Let the POST get underway, then signal.
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Give the cancellation a chance to be felt before letting the handler
	// finish. If shutdown reached the request, the handler returns early and
	// never sets completed.
	time.Sleep(150 * time.Millisecond)
	close(release)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("processDue did not return")
	}

	if !completed.Load() {
		t.Error("shutdown cancelled a delivery that was already in flight; the endpoint " +
			"may have processed it, and panmail will send it again")
	}
}

// The decision to start *another* delivery still follows the parent context. An
// unattempted notification cannot be duplicated by being left, and waiting for
// the whole claimed batch would make every shutdown a full drain.
func TestShutdownStopsStartingNewDeliveries(t *testing.T) {
	var requests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true, Secret: "shhh"})

	w := newWorker(deliveries, subs)
	for i := range 20 {
		w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED,
			map[string]any{"id": i})
	}

	// Already cancelled: nothing should be attempted at all.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w.processDue(ctx)

	if got := requests.Load(); got != 0 {
		t.Errorf("made %d deliveries after the shutdown signal; they should be left for the lease", got)
	}
}
