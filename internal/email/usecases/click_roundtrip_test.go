package usecases

import (
	"context"
	"html"
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

// The joint between the two halves of click tracking, tested the way the
// unsubscribe round trip tests its own.
//
// Both halves are covered alone. tracking_test.go checks that every injected
// link verifies, but it builds the tracking.Link by hand and hands it to the
// signer directly — it never parses the URL it just wrote. The handler's tests
// build their own URLs the same way. So neither side can see the two disagreeing
// about how a link survives the trip through an HTML document and back out of a
// mail client.
//
// If they do disagree the message still sends and still looks right; every
// recipient who clicks gets 403 Invalid tracking link instead of the page they
// were promised, and no click is ever recorded.
//
// So this takes the body the send path actually emits, pulls the href back out
// the way a mail client does, and gives it to the real handler.

type recordingEvents struct {
	eventusecases.ProcessEventUsecase
	mu     sync.Mutex
	clicks []string
}

func (r *recordingEvents) RecordEvent(_ context.Context, _, _, _ string,
	kind panmailv1.EmailEventType, _, _, _ string, meta map[string]any) error {
	if kind == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.clicks = append(r.clicks, meta["url"].(string))
	}
	return nil
}

// hrefValue pulls the first href out of a document, then undoes the HTML
// entity escaping — which is what a mail client does before it makes a request.
// The attribute is markup, so "&amp;" in the source is one "&" on the wire.
var hrefValue = regexp.MustCompile(`(?i)href\s*=\s*"([^"]+)"`)

func clickLinkFromBody(t *testing.T, body string) string {
	t.Helper()
	for _, m := range hrefValue.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], "/track/click/") {
			return html.UnescapeString(m[1])
		}
	}
	t.Fatalf("the send path emitted no click tracking link:\n%s", body)
	return ""
}

func TestALinkFromTheSendPathRedirectsAtTheHandler(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
		// A real campaign link: more than one query parameter, so the "&" that
		// separates them has to survive being nested inside a URL that uses "&"
		// for its own parameters.
		target = "https://shop.example.com/sale?utm_source=email&utm_campaign=spring"
	)

	signer := tracking.NewSigner([]byte("a key for the round trip"))
	sender := &sendEmailUsecase{staticBaseURL: "https://mail.example.com", trackingSigner: signer}

	// Exactly what the delivery loop does to a body, in the same order.
	body := sender.injectTracking(tenantID, messageID, recipient,
		`<html><body><p>Our <a href="`+html.EscapeString(target)+`">spring sale</a> is on.</p></body></html>`)
	body = hardenForOutlook(body)

	link := clickLinkFromBody(t, body)

	events := &recordingEvents{}
	handler := eventhttp.NewTrackingHandler(events, signer)

	rec := httptest.NewRecorder()
	handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, link, nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("following the link the send path emitted returned %d: %s\nlink: %s",
			rec.Code, strings.TrimSpace(rec.Body.String()), link)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Errorf("redirected to %q, want %q — the recipient lands somewhere other than the advertised page", got, target)
	}
	if len(events.clicks) != 1 || events.clicks[0] != target {
		t.Errorf("recorded clicks = %v, want exactly [%s]", events.clicks, target)
	}
}
