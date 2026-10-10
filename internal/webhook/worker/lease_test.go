package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores/postgres"
)

// A claim has to outlast the work done under it. The batch used to be fifty
// sequential POSTs of up to fifteen seconds each under a two-minute lease, and
// nothing stopped a worker starting a POST after its lease had run out: a
// second worker reclaimed the rows and the endpoint got the notification twice.
// Probed with two workers, six of ten deliveries were POSTed twice.

func TestTheLeaseOutlastsTheWorstCaseBatch(t *testing.T) {
	w := NewDurableWorker(newMemDeliveries(), &fakeSubs{})

	// The last attempt may start at the end of the start window; it then
	// runs for at most the client timeout and records its outcome within the
	// save reserve. All of that has to land inside the lease.
	worstCase := w.startWindow() + w.client.Timeout + w.saveReserve
	if worstCase > w.lease {
		t.Errorf("a batch can run %v under a %v lease", worstCase, w.lease)
	}
	if w.startWindow() <= 0 {
		t.Errorf("start window is %v: no attempt could ever start", w.startWindow())
	}
}

func TestTwoWorkersDoNotDeliverTheSameNotificationTwice(t *testing.T) {
	const notifications = 10
	const subscription = "33333333-3333-3333-3333-333333333333"

	var mu sync.Mutex
	posted := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow enough that one worker cannot get through the batch inside the
		// lease the test gives it.
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		posted[r.Header.Get(DeliveryHeader)]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := postgres.NewDeliveryStore(storetest.NewConnection(t))
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: subscription, TenantID: wTenant, URL: server.URL, Active: true, Secret: "shhh"})

	newLeasedWorker := func() *DurableWorker {
		w := NewDurableWorker(deliveries, subs)
		// Generous reserves, so a loaded runner is not mistaken for the bug:
		// the window to start an attempt is 500ms of a 2s lease.
		w.client = &http.Client{Timeout: 500 * time.Millisecond}
		w.lease = 2 * time.Second
		w.saveReserve = time.Second
		return w
	}
	a, b := newLeasedWorker(), newLeasedWorker()

	for range notifications {
		a.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var delivered atomic.Bool
	var wg sync.WaitGroup
	for _, w := range []*DurableWorker{a, b} {
		wg.Go(func() {
			for ctx.Err() == nil && !delivered.Load() {
				w.processDue(ctx)
				time.Sleep(10 * time.Millisecond)
			}
		})
	}

	// Done once every notification has been posted; the workers stop then.
	for ctx.Err() == nil {
		mu.Lock()
		n := len(posted)
		mu.Unlock()
		if n == notifications {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let anything a stale claim would still post arrive before counting.
	time.Sleep(time.Second)
	delivered.Store(true)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(posted) != notifications {
		t.Fatalf("posted %d of %d notifications", len(posted), notifications)
	}
	for id, n := range posted {
		if n != 1 {
			t.Errorf("notification %s was posted %d times", id, n)
		}
	}
}

// One slow endpoint used to hold every notification queued behind it, for
// every tenant, because the batch was delivered one POST at a time.
func TestASlowEndpointDoesNotHoldUpAnother(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	defer close(release)

	var fastDone atomic.Bool
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fastDone.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer fast.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	// The slow subscription is listed first, so its notification is first in
	// the batch -- the position that used to block everything after it.
	subs.set(
		&entities.Webhook{ID: "slow", TenantID: wTenant, URL: slow.URL, Active: true},
		&entities.Webhook{ID: "fast", TenantID: "22222222-2222-2222-2222-222222222222", URL: fast.URL, Active: true},
	)

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)

	go w.processDue(context.Background())

	deadline := time.Now().Add(time.Second)
	for !fastDone.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !fastDone.Load() {
		t.Error("a notification for a working endpoint waited behind one that was not answering")
	}
}

// Rows a batch claims but does not reach go back at once, rather than sitting
// claimed until the lease runs out.
func TestUnattemptedNotificationsAreReleased(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	// No time to start anything.
	w.batchBudget = 0
	for range 3 {
		w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	}
	w.processDue(context.Background())

	for _, row := range deliveries.all() {
		if row.Status != entities.DeliveryStatusPending || row.AttemptCount != 0 {
			t.Errorf("an unattempted notification was left %s after %d attempts", row.Status, row.AttemptCount)
		}
	}
}
