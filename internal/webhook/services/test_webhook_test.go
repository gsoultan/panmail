package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/internal/webhook/samples"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

type oneWebhook struct {
	stores.WebhookRepository
	sub *entities.Webhook
}

func (o oneWebhook) GetByID(_ context.Context, _, id string) (*entities.Webhook, error) {
	if o.sub == nil || o.sub.ID != id {
		return nil, sql.ErrNoRows
	}
	return o.sub, nil
}

// A test notification is signed and shaped exactly like a real one, so an
// endpoint that verifies it will verify real deliveries the same way.
func TestTestWebhookSendsASignedSample(t *testing.T) {
	var got *http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	sub := &entities.Webhook{
		ID:     "wh-1",
		URL:    srv.URL,
		Secret: "s3cret",
		Events: []int32{int32(panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED)},
	}
	svc := NewWebhookService(usecases.NewWebhookUsecase(oneWebhook{sub: sub}))

	res, err := svc.TestWebhook(context.Background(), connect.NewRequest(&panmailv1.TestWebhookRequest{Id: "wh-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.Success || res.Msg.StatusCode != http.StatusAccepted || res.Msg.ResponseBody != "ok" {
		t.Fatalf("unexpected result: %+v", res.Msg)
	}
	// Defaults to the first event the subscription listens for.
	if res.Msg.Event != panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED {
		t.Fatalf("sent %v, want the subscription's first event", res.Msg.Event)
	}
	if got.Header.Get("X-Panmail-Test") != "true" {
		t.Error("a test notification must say it is one")
	}
	if got.Header.Get("X-Panmail-Event") != "WEBHOOK_TRIGGER_EVENT_MAIL_BOUNCED" {
		t.Errorf("event header = %q", got.Header.Get("X-Panmail-Event"))
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(got.Header.Get("X-Panmail-Timestamp") + "."))
	mac.Write(gotBody)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); got.Header.Get("X-Panmail-Signature") != want {
		t.Error("signature does not verify against the body that was sent")
	}
	if string(gotBody) != res.Msg.Payload {
		t.Error("the payload reported is not the one that was sent")
	}
}

// An endpoint refusing the test is the answer the user asked for, reported
// as a result rather than as an error from the call.
func TestTestWebhookReportsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
	}))
	defer srv.Close()

	svc := NewWebhookService(usecases.NewWebhookUsecase(oneWebhook{sub: &entities.Webhook{ID: "wh-1", URL: srv.URL}}))
	res, err := svc.TestWebhook(context.Background(), connect.NewRequest(&panmailv1.TestWebhookRequest{
		Id:    "wh-1",
		Event: panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_HELD,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.Success || res.Msg.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Msg.ResponseBody, "bad signature") {
		t.Fatalf("unexpected result: %+v", res.Msg)
	}
}

func TestTestWebhookUnknownSubscriptionIsNotFound(t *testing.T) {
	svc := NewWebhookService(usecases.NewWebhookUsecase(oneWebhook{}))
	_, err := svc.TestWebhook(context.Background(), connect.NewRequest(&panmailv1.TestWebhookRequest{Id: "missing"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("got %v, want NotFound", err)
	}
}

// Every event a subscription can pick has a sample, in the envelope a real
// notification uses.
func TestEveryEventHasASample(t *testing.T) {
	svc := NewWebhookService(usecases.NewWebhookUsecase(oneWebhook{}))
	res, err := svc.ListWebhookEventSamples(context.Background(), connect.NewRequest(&panmailv1.ListWebhookEventSamplesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.Samples) != len(panmailv1.WebhookTriggerEvent_name)-1 {
		t.Fatalf("got %d samples, want one per event", len(res.Msg.Samples))
	}
	for _, s := range res.Msg.Samples {
		var body struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(s.Payload), &body); err != nil {
			t.Fatalf("%v: payload is not JSON: %v", s.Event, err)
		}
		if body.Event != s.Event.String() {
			t.Errorf("%v: envelope names %q", s.Event, body.Event)
		}
		if len(body.Data) == 0 || string(body.Data) == "null" {
			t.Errorf("%v: no sample data", s.Event)
		}
		if s.Description == "" || samples.Description(s.Event) == "" {
			t.Errorf("%v: no description", s.Event)
		}
	}
}
