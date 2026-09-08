package usecases

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
	eventhttp "github.com/gsoultan/panmail/internal/event/transports/http"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// The scenario that produced the bug report, end to end.
//
// A message goes out signed with the placeholder key a pre-setup instance used.
// panmail restarts, derives the real key, and every one of those links stops
// verifying. The messages are delivered; there is nothing to reissue.
//
// The stored copy of the message is what gets them working again: it still
// contains the destination, so the redirect is confined to a URL this tenant
// actually sent even though the signature means nothing any more.
type oneStoredMessage struct {
	tenantID, messageID, body string
}

func (s *oneStoredMessage) GetMessage(_ context.Context, tenantID, messageID string) (*evententities.EmailMessage, error) {
	if tenantID != s.tenantID || messageID != s.messageID {
		return nil, nil
	}
	return &evententities.EmailMessage{ID: messageID, TenantID: tenantID, BodyHTML: s.body}, nil
}

func TestLinksSignedByTheDeadPlaceholderKeyStillWork(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
		target    = "https://shop.example.com/sale?utm_source=email&utm_campaign=spring"
	)

	// What every pre-setup instance signed with, verbatim from v1.5.7.
	deadKey := tracking.NewSigner([]byte("panmail-unconfigured-tracking-key"))

	original := `<html><body><a href="` + html.EscapeString(target) + `">buy</a></body></html>`
	sender := &sendEmailUsecase{staticBaseURL: "https://mail.example.com", trackingSigner: deadKey}
	body := hardenForOutlook(sender.injectTracking(tenantID, messageID, recipient, original))
	link := clickLinkFromBody(t, body)

	// The instance now holds the real key, so the signature is worthless.
	liveKey := tracking.NewSigner(tracking.DeriveKey("the-real-symmetric-key"))
	if err := liveKey.Verify(tracking.Link{
		Kind: trackingKindClick, TenantID: tenantID, MessageID: messageID,
		Recipient: recipient, TargetURL: target,
	}, "whatever"); err == nil {
		t.Fatal("the premise is wrong: the live key accepted a signature it should not")
	}

	events := &recordingEvents{}
	handler := eventhttp.NewTrackingHandler(events, liveKey).
		// The body panmail stored when it sent the message, pre-injection.
		WithSentMessages(&oneStoredMessage{tenantID: tenantID, messageID: messageID, body: original})

	rec := httptest.NewRecorder()
	handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, link, nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("got %d (%s) — the recipient is still stranded\nlink: %s",
			rec.Code, strings.TrimSpace(rec.Body.String()), link)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Errorf("redirected to %q, want %q", got, target)
	}
}
