package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// A link outliving the key that signed it.
//
// Every link in every message already delivered stops verifying the moment the
// key behind it changes — a rotation, a restore that brought the database but
// not the config, a second instance started from a different one. Those
// messages are in inboxes and cannot be reissued, so the recipient who clicks
// gets a 403 and stays there for as long as the mail exists.
//
// The stored copy of the message answers the same question the signature does:
// did panmail put this destination in this message? That is what these tests
// hold, in both directions — because the direction that matters more is the
// one where the answer is no.

type storedMessages struct {
	byID map[string]*entities.EmailMessage
	err  error
}

func (s *storedMessages) GetMessage(_ context.Context, tenantID, messageID string) (*entities.EmailMessage, error) {
	if s.err != nil {
		return nil, s.err
	}
	m := s.byID[tenantID+"|"+messageID]
	return m, nil
}

func messagesContaining(body string) *storedMessages {
	return &storedMessages{byID: map[string]*entities.EmailMessage{
		trkTenantID + "|" + trkMessageID: {
			ID: trkMessageID, TenantID: trkTenantID, BodyHTML: body,
		},
	}}
}

const sentBody = `<html><body>
  <p>Our <a href="https://shop.example.com/sale?utm_source=email&amp;utm_campaign=spring">spring sale</a> is on.</p>
  <p><a href="https://shop.example.com/terms">Terms</a></p>
</body></html>`

func TestALinkWhoseKeyIsGoneIsStillFollowed(t *testing.T) {
	handler, usecase, _ := newTrackingTestHandler()
	handler.WithSentMessages(messagesContaining(sentBody))

	// What a link signed by a key this instance no longer holds looks like.
	const target = "https://shop.example.com/sale?utm_source=email&utm_campaign=spring"
	deadSignature := "Zm9yZ290dGVua2V5c2lnbmF0dXJlZm9yYXRlc3Q"

	rec := httptest.NewRecorder()
	handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, clickPath(target, deadSignature), nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("got %d, want 302 — a recipient clicking a link from mail we actually "+
			"sent is stranded on an error nobody can clear", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Errorf("redirected to %q, want %q", got, target)
	}
	if len(usecase.events) != 1 {
		t.Errorf("the click was not recorded (%d events)", len(usecase.events))
	}
}

// The one that matters. The signature is the only thing stopping this endpoint
// being an open redirect on the domain recipients are taught to trust, and the
// key that signed the old links is a string literal in a public repository — so
// anyone can produce a signature this handler used to accept. What stops them
// is that the destination has to be one the tenant actually sent.
func TestADestinationWeNeverSentIsRefusedHoweverItIsSigned(t *testing.T) {
	for _, target := range []string{
		"https://evil.example.com/phish",
		// A near miss: right host, path we never linked to.
		"https://shop.example.com/admin",
		// Right prefix, extra parameter.
		"https://shop.example.com/sale?utm_source=email&utm_campaign=spring&x=1",
		// Right link, wrong scheme.
		"http://shop.example.com/terms",
	} {
		t.Run(target, func(t *testing.T) {
			handler, usecase, _ := newTrackingTestHandler()
			handler.WithSentMessages(messagesContaining(sentBody))

			rec := httptest.NewRecorder()
			handler.HandleClick(rec, httptest.NewRequest(http.MethodGet,
				clickPath(target, "YW55b25lY2FuZm9yZ2V0aGVvbGRrZXlzaWc"), nil))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("got %d, want 403 — %q is not a destination this tenant sent, "+
					"and following it makes this an open redirect on their sending domain",
					rec.Code, target)
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Errorf("redirected to %q anyway", loc)
			}
			if len(usecase.events) != 0 {
				t.Errorf("a forged click was recorded: %v", usecase.events)
			}
		})
	}
}

// Fails closed in every way it can fail.
func TestTheFallbackFailsClosed(t *testing.T) {
	const target = "https://shop.example.com/terms"

	cases := map[string]func(h *TrackingHandler){
		"no lookup wired": func(h *TrackingHandler) {},
		"message not found": func(h *TrackingHandler) {
			h.WithSentMessages(&storedMessages{byID: map[string]*entities.EmailMessage{}})
		},
		"lookup errors":       func(h *TrackingHandler) { h.WithSentMessages(&storedMessages{err: context.DeadlineExceeded}) },
		"body already pruned": func(h *TrackingHandler) { h.WithSentMessages(messagesContaining("")) },
		"body has no links":   func(h *TrackingHandler) { h.WithSentMessages(messagesContaining(`<p>no links here</p>`)) },
	}

	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			handler, _, _ := newTrackingTestHandler()
			wire(handler)

			rec := httptest.NewRecorder()
			handler.HandleClick(rec, httptest.NewRequest(http.MethodGet, clickPath(target, "bm90YXNpZ25hdHVyZQ"), nil))

			if rec.Code != http.StatusForbidden {
				t.Errorf("got %d, want 403", rec.Code)
			}
		})
	}
}

// A correctly signed link must not start depending on the stored message, or
// tracking would break the moment retention removed a body.
func TestAValidSignatureDoesNotNeedTheStoredMessage(t *testing.T) {
	handler, usecase, signer := newTrackingTestHandler()
	handler.WithSentMessages(&storedMessages{byID: map[string]*entities.EmailMessage{}})

	const target = "https://shop.example.com/anything-at-all"
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
		t.Fatalf("got %d, want 302 — a signed link must work with no stored body", rec.Code)
	}
	if len(usecase.events) != 1 {
		t.Errorf("the click was not recorded")
	}
}
