package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

// With the operator's restriction on, a target on a private address is
// refused when the connection is made -- after resolution, so a name pointing
// there at delivery time is caught however it resolved when it was saved --
// and the notification fails rather than retrying against a policy that will
// not change.
func TestARestrictedWorkerDoesNotReachAPrivateTarget(t *testing.T) {
	var reached atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	port := server.URL[strings.LastIndex(server.URL, ":")+1:]
	for _, target := range []string{
		server.URL,                          // a loopback literal
		"http://localhost:" + port,          // a name that resolves to loopback
		"http://[::ffff:127.0.0.1]:" + port, // the mapped form
	} {
		t.Run(target, func(t *testing.T) {
			deliveries := newMemDeliveries()
			subs := &fakeSubs{}
			subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: target, Active: true})

			w := newWorker(deliveries, subs)
			w.RestrictEgress()
			w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
			w.processDue(context.Background())

			row := deliveries.all()[0]
			if row.Status != entities.DeliveryStatusFailed {
				t.Errorf("status = %q, want failed: a refused target is not worth retrying", row.Status)
			}
			if !strings.Contains(row.LastError, "private or reserved") {
				t.Errorf("last error %q does not say why", row.LastError)
			}
		})
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the private target was reached %d times", n)
	}
}

// Off by default: a self-hosted gateway posting to a service beside it keeps
// working.
func TestAnUnrestrictedWorkerStillReachesAPrivateTarget(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: server.URL, Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	if !reached.Load() || deliveries.all()[0].Status != entities.DeliveryStatusDelivered {
		t.Error("a private target was refused with no restriction configured")
	}
}
