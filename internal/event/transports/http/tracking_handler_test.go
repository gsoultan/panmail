package http

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

const (
	trkTenantID  = "tenant-1"
	trkMessageID = "message-1"
	trkRecipient = "user@example.com"
)

type recordedEvent struct {
	tenantID  string
	messageID string
	eventType panmailv1.EmailEventType
	recipient string
	metadata  map[string]any
}

// recordingUsecase captures RecordEvent calls. The interface is embedded so
// that any other method being reached fails loudly rather than silently.
type recordingUsecase struct {
	usecases.ProcessEventUsecase
	events []recordedEvent
}

func (m *recordingUsecase) RecordEvent(
	ctx context.Context,
	tenantID, providerID, messageID string,
	eventType panmailv1.EmailEventType,
	recipient string, subject string, errorMessage string,
	metadata map[string]any,
) error {
	m.events = append(m.events, recordedEvent{
		tenantID:  tenantID,
		messageID: messageID,
		eventType: eventType,
		recipient: recipient,
		metadata:  metadata,
	})
	return nil
}

func newTrackingTestHandler() (*TrackingHandler, *recordingUsecase, *tracking.Signer) {
	signer := tracking.NewSigner([]byte("tracking-handler-test-key-value!"))
	usecase := &recordingUsecase{}
	return NewTrackingHandler(usecase, signer), usecase, signer
}

func encodedRecipient() string {
	return base64.RawURLEncoding.EncodeToString([]byte(trkRecipient))
}

func openPath(signature string) string {
	path := "/track/open/" + trkTenantID + "/" + trkMessageID + "/" + encodedRecipient()
	if signature != "" {
		path += "?" + tracking.SignatureParam + "=" + url.QueryEscape(signature)
	}
	return path
}

func clickPath(target, signature string) string {
	path := "/track/click/" + trkTenantID + "/" + trkMessageID + "/" + encodedRecipient() +
		"?url=" + url.QueryEscape(target)
	if signature != "" {
		path += "&" + tracking.SignatureParam + "=" + url.QueryEscape(signature)
	}
	return path
}

func TestHandleOpenRecordsOnlySignedRequests(t *testing.T) {
	_, _, signer := newTrackingTestHandler()

	valid := signer.Sign(tracking.Link{
		Kind:      trackingKindOpen,
		TenantID:  trkTenantID,
		MessageID: trkMessageID,
		Recipient: trkRecipient,
	})

	otherTenant := signer.Sign(tracking.Link{
		Kind:      trackingKindOpen,
		TenantID:  "tenant-2",
		MessageID: trkMessageID,
		Recipient: trkRecipient,
	})

	tests := []struct {
		name       string
		signature  string
		wantRecord bool
	}{
		{"genuine signature", valid, true},
		{"no signature", "", false},
		{"garbage signature", "not-a-signature", false},
		{"signature for another tenant", otherTenant, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, usecase, _ := newTrackingTestHandler()

			rec := httptest.NewRecorder()
			handler.HandleOpen(rec, httptest.NewRequest(http.MethodGet, openPath(tc.signature), nil))

			// The response is identical either way: it must not tell a prober
			// whether the link was genuine.
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "image/gif" {
				t.Errorf("expected a gif, got %q", got)
			}
			if rec.Body.Len() == 0 {
				t.Error("expected a pixel body")
			}

			if recorded := len(usecase.events) == 1; recorded != tc.wantRecord {
				t.Errorf("recorded = %v; want %v (%d events)", recorded, tc.wantRecord, len(usecase.events))
			}

			if tc.wantRecord {
				e := usecase.events[0]
				if e.eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED {
					t.Errorf("expected an OPENED event, got %s", e.eventType)
				}
				if e.tenantID != trkTenantID || e.messageID != trkMessageID || e.recipient != trkRecipient {
					t.Errorf("event attributed incorrectly: %+v", e)
				}
			}
		})
	}
}

// The regression that matters most: /track/click must never forward to a
// destination it did not sign, or it is an open redirect on the domain
// recipients are taught to trust.
func TestHandleClickRefusesToRedirectWithoutAValidSignature(t *testing.T) {
	_, _, signer := newTrackingTestHandler()

	const genuineTarget = "https://example.com/promo"
	const attackerTarget = "https://evil.example.net/phish"

	genuineSig := signer.Sign(tracking.Link{
		Kind:      trackingKindClick,
		TenantID:  trkTenantID,
		MessageID: trkMessageID,
		Recipient: trkRecipient,
		TargetURL: genuineTarget,
	})

	tests := []struct {
		name     string
		target   string
		sig      string
		wantCode int
	}{
		{"no signature at all", attackerTarget, "", http.StatusForbidden},
		{"invalid signature", attackerTarget, "forged", http.StatusForbidden},
		{"target swapped after signing", attackerTarget, genuineSig, http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, usecase, _ := newTrackingTestHandler()

			rec := httptest.NewRecorder()
			handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, clickPath(tc.target, tc.sig), nil))

			if rec.Code != tc.wantCode {
				t.Errorf("expected %d, got %d", tc.wantCode, rec.Code)
			}
			if location := rec.Header().Get("Location"); location != "" {
				t.Errorf("must not redirect, but sent Location: %s", location)
			}
			if len(usecase.events) != 0 {
				t.Errorf("must not record an event, recorded %d", len(usecase.events))
			}
		})
	}
}

func TestHandleClickRedirectsWhenSigned(t *testing.T) {
	handler, usecase, signer := newTrackingTestHandler()

	const target = "https://example.com/promo?utm=1"
	sig := signer.Sign(tracking.Link{
		Kind:      trackingKindClick,
		TenantID:  trkTenantID,
		MessageID: trkMessageID,
		Recipient: trkRecipient,
		TargetURL: target,
	})

	rec := httptest.NewRecorder()
	handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, clickPath(target, sig), nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Errorf("expected a redirect to %s, got %s", target, got)
	}

	if len(usecase.events) != 1 {
		t.Fatalf("expected one event, got %d", len(usecase.events))
	}
	e := usecase.events[0]
	if e.eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED {
		t.Errorf("expected a CLICKED event, got %s", e.eventType)
	}
	if e.metadata["url"] != target {
		t.Errorf("expected the target in metadata, got %v", e.metadata["url"])
	}
}

// A signature proves we generated the link, not that the destination is a safe
// kind of URL. A tenant putting javascript: in their own template must not get
// it served from the gateway's domain.
func TestHandleClickRejectsUnsafeSchemesEvenWhenSigned(t *testing.T) {
	unsafe := []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg",
		"file:///etc/passwd",
	}

	for _, target := range unsafe {
		t.Run(target, func(t *testing.T) {
			handler, usecase, signer := newTrackingTestHandler()

			sig := signer.Sign(tracking.Link{
				Kind:      trackingKindClick,
				TenantID:  trkTenantID,
				MessageID: trkMessageID,
				Recipient: trkRecipient,
				TargetURL: target,
			})

			rec := httptest.NewRecorder()
			handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, clickPath(target, sig), nil))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", rec.Code)
			}
			if location := rec.Header().Get("Location"); location != "" {
				t.Errorf("must not redirect, but sent Location: %s", location)
			}
			if len(usecase.events) != 0 {
				t.Errorf("must not record an event, recorded %d", len(usecase.events))
			}
		})
	}
}

func TestHandleClickRejectsMalformedRequests(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"too few path segments", "/track/click/tenant?url=https://example.com", http.StatusBadRequest},
		{"missing target", "/track/click/" + trkTenantID + "/" + trkMessageID + "/" + encodedRecipient(), http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, usecase, _ := newTrackingTestHandler()

			rec := httptest.NewRecorder()
			handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantCode {
				t.Errorf("expected %d, got %d", tc.wantCode, rec.Code)
			}
			if len(usecase.events) != 0 {
				t.Errorf("must not record an event, recorded %d", len(usecase.events))
			}
		})
	}
}
