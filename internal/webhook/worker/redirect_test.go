package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
)

// The delivery client followed redirects anywhere, so an endpoint answering
// 307 sent the signed notification -- payload, signature and delivery id -- on
// to a host the tenant never named.

func TestARedirectToAnotherHostIsNotFollowed(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: endpoint.URL, Active: true, Secret: "shhh"})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	if reached.Load() {
		t.Error("the notification was carried to a host the subscription does not name")
	}
	if row := deliveries.all()[0]; row.Status != entities.DeliveryStatusFailed {
		t.Errorf("status = %q, want failed: the endpoint answered with a redirect, not an acceptance", row.Status)
	}
}

// A redirect on the same host -- a trailing slash, a moved path -- is the
// endpoint's own business and still followed.
func TestARedirectOnTheSameHostIsFollowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	endpoint := httptest.NewServer(mux)
	defer endpoint.Close()

	deliveries := newMemDeliveries()
	subs := &fakeSubs{}
	subs.set(&entities.Webhook{ID: "sub-1", TenantID: wTenant, URL: endpoint.URL + "/old", Active: true})

	w := newWorker(deliveries, subs)
	w.Enqueue(wTenant, panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT, nil)
	w.processDue(context.Background())

	if row := deliveries.all()[0]; row.Status != entities.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", row.Status)
	}
}
