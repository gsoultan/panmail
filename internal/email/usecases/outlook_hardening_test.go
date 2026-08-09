package usecases

import (
	"context"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// The builder is not the only path to a sent message: HTML also arrives from a
// file upload and straight from the API as body_html. Those used to reach the
// recipient with no Outlook handling at all, while builder-made mail got all of
// it — so the same campaign rendered differently depending on how it was
// authored.

func TestRawHtmlIsHardenedOnTheWayOut(t *testing.T) {
	u, sender := newUsecaseWithBaseURL("https://mail.example.com")

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		// What an API caller or an uploaded file looks like: no namespaces, no
		// DPI settings, none of the table spacing fixes.
		BodyHtml: `<html><head><title>t</title></head><body><p>hi</p></body></html>`,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := string(sender.sentEmails[0].HTMLBody)
	for _, marker := range []string{"xmlns:v=", "xmlns:o=", "PixelsPerInch", "mso-table-lspace"} {
		if !strings.Contains(got, marker) {
			t.Errorf("raw HTML reached the recipient without %s", marker)
		}
	}
}

// Builder output is already hardened and says so. Converting it again appends a
// second OfficeDocumentSettings block and two more stylesheets — about 3KB of
// duplication with competing rules — for nothing.
func TestBuilderOutputIsNotHardenedTwice(t *testing.T) {
	u, sender := newUsecaseWithBaseURL("https://mail.example.com")

	body := `<!DOCTYPE html>
<!--gsmail:outlook-->
<html lang="en" xmlns:v="urn:schemas-microsoft-com:vml"><head><title>t</title>
<style>li { text-indent: -1em; }</style></head><body><p>hi</p></body></html>`

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		BodyHtml:   body,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := string(sender.sentEmails[0].HTMLBody)
	if n := strings.Count(got, "PixelsPerInch"); n > 0 {
		t.Errorf("an already-hardened document gained %d OfficeDocumentSettings blocks", n)
	}
	if n := strings.Count(got, "xmlns:v="); n != 1 {
		t.Errorf("xmlns:v appears %d times, want 1", n)
	}
}

// Hardening must not disturb the tracking already injected into the body.
func TestHardeningPreservesTrackingAndLinks(t *testing.T) {
	u, sender := newUsecaseWithBaseURL("https://mail.example.com")

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		BodyHtml:   `<html><body><a href="https://example.com/x">link</a></body></html>`,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := string(sender.sentEmails[0].HTMLBody)
	if !strings.Contains(got, "/track/click/") {
		t.Error("the click tracking link did not survive hardening")
	}
	if !strings.Contains(got, "/track/open/") {
		t.Error("the open pixel did not survive hardening")
	}
}

// A plain-text-only message has no HTML to harden, and must not gain any.
func TestATextOnlyMessageStaysTextOnly(t *testing.T) {
	u, sender := newUsecaseWithBaseURL("https://mail.example.com")

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	if _, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "from@example.com",
		To:         []string{"to@example.com"},
		Subject:    "Hello",
		BodyText:   "just text",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if body := string(sender.sentEmails[0].HTMLBody); body != "" {
		t.Errorf("a text-only message gained an HTML part: %q", body)
	}
}
