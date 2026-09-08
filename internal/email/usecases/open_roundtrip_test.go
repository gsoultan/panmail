package usecases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	eventhttp "github.com/gsoultan/panmail/internal/event/transports/http"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// The third joint, and the one that cannot report its own failure.
//
// Click has a round trip and unsubscribe has a round trip, both of which can
// assert on a status code: a rejected click answers 403, a rejected unsubscribe
// answers 4xx. The open pixel answers 200 and a transparent GIF whatever
// happens, deliberately — the response must not tell a prober whether a link
// was genuine — so if the send path and the handler ever disagree about how an
// open link is shaped, every open is dropped and *nothing observable changes*.
// No error, no status, no bounce. The open rate goes to zero and stays there.
//
// That is also what made the signing-key bug so expensive: clicks at least
// answered 403, while opens simply stopped and nobody could tell.
//
// So this asserts on the recorded event, which is the only evidence there is.

var pixelSrc = regexp.MustCompile(`<img src="([^"]+)"`)

// openPixelFromBody pulls the pixel URL out the way a mail client does when it
// loads remote images.
func openPixelFromBody(t *testing.T, body string) string {
	t.Helper()
	m := pixelSrc.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the send path emitted no open pixel:\n%s", body)
	}
	return unescapeHrefValue(m[1])
}

func TestAnOpenPixelFromTheSendPathRecordsAtTheHandler(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
	)

	signer := tracking.NewSigner(tracking.DeriveKey("a key for the round trip"))
	sender := &sendEmailUsecase{staticBaseURL: "https://mail.example.com", trackingSigner: signer}

	body := sender.injectTracking(tenantID, messageID, recipient,
		`<html><body><p>Hello.</p></body></html>`)
	body = hardenForOutlook(body)

	pixel := openPixelFromBody(t, body)

	events := &recordingOpens{}
	rec := httptest.NewRecorder()
	eventhttp.NewTrackingHandler(events, signer).
		HandleOpen(rec, httptest.NewRequest(http.MethodGet, pixel, nil))

	// The pixel is always served; it is not the signal.
	if rec.Code != http.StatusOK {
		t.Fatalf("the pixel must be served whatever happens, got %d", rec.Code)
	}

	if len(events.opens) != 1 {
		t.Fatalf("the open was not recorded (%d events) — the send path and the handler "+
			"disagree about how an open link is shaped, and nothing about the response "+
			"would ever have told you\npixel: %s", len(events.opens), pixel)
	}
	if got := events.opens[0]; got != recipient {
		t.Errorf("open recorded against %q, want %q", got, recipient)
	}
}

// An unsigned or altered pixel must record nothing, or anyone could inflate any
// tenant's open rate by guessing a URL.
func TestAnAlteredOpenPixelRecordsNothing(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
	)

	signer := tracking.NewSigner(tracking.DeriveKey("a key for the round trip"))
	sender := &sendEmailUsecase{staticBaseURL: "https://mail.example.com", trackingSigner: signer}

	body := sender.injectTracking(tenantID, messageID, recipient,
		`<html><body><p>Hello.</p></body></html>`)
	pixel := openPixelFromBody(t, body)

	for _, tc := range []struct{ name, url string }{
		{"signature dropped", pixel[:strings.Index(pixel, "?")]},
		{"signature altered", strings.Replace(pixel, "sig=", "sig=x", 1)},
		{"recipient swapped", strings.Replace(pixel,
			encodeRecipientForTest(recipient), encodeRecipientForTest("victim@example.org"), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &recordingOpens{}
			rec := httptest.NewRecorder()
			eventhttp.NewTrackingHandler(events, signer).
				HandleOpen(rec, httptest.NewRequest(http.MethodGet, tc.url, nil))

			if rec.Code != http.StatusOK {
				t.Errorf("the pixel must still be served, got %d — the response must not "+
					"tell a prober whether a link was genuine", rec.Code)
			}
			if len(events.opens) != 0 {
				t.Errorf("an unsigned open was recorded as %v", events.opens)
			}
		})
	}
}

type recordingOpens struct {
	eventusecases.ProcessEventUsecase
	mu    sync.Mutex
	opens []string
}

func (r *recordingOpens) RecordEvent(_ context.Context, _, _, _ string,
	kind panmailv1.EmailEventType, recipient, _, _ string, _ map[string]any) error {
	if kind == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.opens = append(r.opens, recipient)
	}
	return nil
}
