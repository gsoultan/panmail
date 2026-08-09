package http

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/storetest"
	suppressionpostgres "github.com/gsoultan/panmail/internal/suppression/repositories/stores/postgres"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

const (
	testMessageID = "aaaaaaaa-0000-0000-0000-000000000001"
	testRecipient = "reader@example.com"
)

func newUnsubscribeHandler(t *testing.T) (*UnsubscribeHandler, suppressionusecases.ManageSuppressionsUsecase, *tracking.Signer) {
	t.Helper()
	suppressions := suppressionusecases.NewManageSuppressionsUsecase(
		suppressionpostgres.NewStore(storetest.NewConnection(t)))
	signer := tracking.NewSigner([]byte("test-unsubscribe-key"))
	// The event usecase is optional on this handler; suppression is the part
	// that must work.
	return NewUnsubscribeHandler(suppressions, nil, signer), suppressions, signer
}

func unsubscribeURL(signer *tracking.Signer, recipient string) string {
	sig := signer.Sign(tracking.Link{
		Kind:      trackingKindUnsubscribe,
		TenantID:  storetest.TenantA,
		MessageID: testMessageID,
		Recipient: recipient,
	})
	return "/unsubscribe/" + storetest.TenantA + "/" + testMessageID + "/" +
		base64.RawURLEncoding.EncodeToString([]byte(recipient)) +
		"?" + tracking.SignatureParam + "=" + sig
}

func isSuppressed(t *testing.T, u suppressionusecases.ManageSuppressionsUsecase, addr string) bool {
	t.Helper()
	suppressed, _, err := u.Check(context.Background(), storetest.TenantA, addr)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return suppressed
}

// RFC 8058: the provider POSTs, and the address must be suppressed immediately
// with no confirmation step. A confirmation page counts as a failed unsubscribe.
func TestPostUnsubscribesImmediately(t *testing.T) {
	h, suppressions, signer := newUnsubscribeHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, unsubscribeURL(signer, testRecipient), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !isSuppressed(t, suppressions, testRecipient) {
		t.Error("the address was not suppressed")
	}
	if strings.Contains(rec.Body.String(), "<form") {
		t.Error("the POST response asks for confirmation; providers read that as a failed unsubscribe")
	}
}

// The one that matters most. Mail clients, spam filters and link-scanning
// security software fetch every URL in a message. If GET unsubscribed, they
// would unsubscribe recipients who never clicked.
func TestGetDoesNotUnsubscribe(t *testing.T) {
	h, suppressions, signer := newUnsubscribeHandler(t)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, unsubscribeURL(signer, testRecipient), nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", method, rec.Code)
		}
		if isSuppressed(t, suppressions, testRecipient) {
			t.Fatalf("%s unsubscribed the recipient; a link scanner would silently opt them out", method)
		}
	}
}

func TestGetOffersAFormThatPosts(t *testing.T) {
	h, _, signer := newUnsubscribeHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, unsubscribeURL(signer, testRecipient), nil))

	body := rec.Body.String()
	if !strings.Contains(strings.ToUpper(body), `METHOD="POST"`) {
		t.Error("the confirmation page must submit by POST")
	}
	if !strings.Contains(body, testRecipient) {
		t.Error("the page should show which address is being unsubscribed")
	}
}

// Without a signature check the endpoint is a way to suppress any address for
// any tenant, silently stopping their mail.
func TestAnUnsignedOrAlteredLinkIsRefused(t *testing.T) {
	h, suppressions, signer := newUnsubscribeHandler(t)

	valid := unsubscribeURL(signer, testRecipient)
	cases := map[string]string{
		"no signature":       strings.Split(valid, "?")[0],
		"wrong signature":    strings.Split(valid, "?")[0] + "?" + tracking.SignatureParam + "=deadbeef",
		"swapped recipient":  strings.Replace(valid, base64.RawURLEncoding.EncodeToString([]byte(testRecipient)), base64.RawURLEncoding.EncodeToString([]byte("victim@example.com")), 1),
		"swapped message id": strings.Replace(valid, testMessageID, "bbbbbbbb-0000-0000-0000-000000000002", 1),
	}

	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))

			if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
				t.Errorf("expected the request to be refused, got %d", rec.Code)
			}
		})
	}

	if isSuppressed(t, suppressions, testRecipient) {
		t.Error("a tampered link suppressed the recipient")
	}
	if isSuppressed(t, suppressions, "victim@example.com") {
		t.Error("a tampered link suppressed an unrelated address")
	}
}

// A signature is bound to its tenant, so a link issued by one tenant must not
// suppress an address for another.
func TestALinkCannotBeReusedForAnotherTenant(t *testing.T) {
	h, _, signer := newUnsubscribeHandler(t)

	url := strings.Replace(unsubscribeURL(signer, testRecipient), storetest.TenantA, storetest.TenantB, 1)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))

	if rec.Code == http.StatusOK {
		t.Error("a link signed for one tenant was accepted for another")
	}
}

// Providers retry, and a mailbox may be unsubscribed twice. The second attempt
// must still report success, or the provider records a failed unsubscribe.
func TestUnsubscribingTwiceStillSucceeds(t *testing.T) {
	h, suppressions, signer := newUnsubscribeHandler(t)

	for i := range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, unsubscribeURL(signer, testRecipient), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i+1, rec.Code)
		}
	}
	if !isSuppressed(t, suppressions, testRecipient) {
		t.Error("the address should still be suppressed")
	}
}

// The address is reflected into the confirmation page.
func TestTheConfirmationPageEscapesTheAddress(t *testing.T) {
	h, _, signer := newUnsubscribeHandler(t)

	nasty := `x"><script>alert(1)</script>@example.com`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, unsubscribeURL(signer, nasty), nil))

	if strings.Contains(rec.Body.String(), "<script>") {
		t.Error("the recipient address was reflected into the page unescaped")
	}
}

func TestAnUnsupportedMethodIsRejected(t *testing.T) {
	h, _, signer := newUnsubscribeHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, unsubscribeURL(signer, testRecipient), nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
	if rec.Header().Get("Allow") == "" {
		t.Error("a 405 must say which methods are allowed")
	}
}

// The suppression the endpoint creates has to be the one the send path finds,
// which means it must be keyed the same way.
func TestTheSuppressionIsFoundUnderAnySpelling(t *testing.T) {
	h, suppressions, signer := newUnsubscribeHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, unsubscribeURL(signer, "Reader@Example.COM"), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if !isSuppressed(t, suppressions, "reader@example.com") {
		t.Error("the unsubscribe did not match the address the send path would look up")
	}
}

var _ = panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED
