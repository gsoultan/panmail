package usecases

import (
	"context"
	"errors"
	"strings"
	"testing"

	gsmail "github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

// The reply fields have to survive rendering onto the wire on every copy, and
// an inline image has to go out as an inline part its cid: can resolve.
func TestReplyHeadersAndInlineImagesReachTheWire(t *testing.T) {
	provider := &providerEntities.EmailProvider{
		ID: testProviderID, Name: "SMTP", Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
	}
	sender := &mockSender{}
	u := NewSendEmailUsecase(SendEmailDeps{
		ProviderRepo:    &mockProviderRepo{provider: provider},
		EventUsecase:    &mockEventUsecase{},
		ProviderFactory: &mockFactory{sender: sender},
		Renderer:        NewTemplateRenderer(),
	})

	ctx := context.WithValue(context.Background(), SkipOutboxKey, true)
	_, err := u.SendEmail(ctx, testTenantID, &panmailv1.SendEmailRequest{
		ProviderId:                testProviderID,
		From:                      "from@example.com",
		To:                        []string{"a@example.com", "b@example.com"},
		Subject:                   "RE: Plans",
		BodyHtml:                  `<img src="cid:logo@x">`,
		ReplyTo:                   "sales@example.com",
		InReplyTo:                 "<orig@example.net>",
		References:                []string{"<root@example.net>", "<orig@example.net>"},
		DispositionNotificationTo: "from@example.com",
		Attachments: []*panmailv1.Attachment{
			{Filename: "logo.png", ContentType: "image/png", Content: []byte("png"), ContentId: "logo@x"},
		},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(sender.sentEmails) != 2 {
		t.Fatalf("sent %d copies, want 2", len(sender.sentEmails))
	}

	for _, copy := range sender.sentEmails {
		raw, err := gsmail.RenderMessage(copy)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		parsed, err := gsmail.ParseRawEmail(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		for name, want := range map[string]string{
			"In-Reply-To":                 "<orig@example.net>",
			"References":                  "<root@example.net> <orig@example.net>",
			"Disposition-Notification-To": "from@example.com",
		} {
			if got := parsed.Header(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if !strings.Contains(parsed.ReplyTo, "sales@example.com") {
			t.Errorf("Reply-To = %q, want sales@example.com", parsed.ReplyTo)
		}
		if !strings.Contains(string(raw), "Content-ID: <logo@x>") {
			t.Errorf("the image was not sent as an inline part with its Content-ID")
		}
	}
}

func TestSendRefusesReplyHeadersItCannotSend(t *testing.T) {
	long := "<" + strings.Repeat("a", MaxMessageIDLength) + "@x>"
	tooMany := make([]string, MaxReferences+1)
	for i := range tooMany {
		tooMany[i] = "<a@example.net>"
	}

	testCases := []struct {
		name string
		edit func(*panmailv1.SendEmailRequest)
	}{
		{"reply-to not an address", func(r *panmailv1.SendEmailRequest) { r.ReplyTo = "nope" }},
		{"receipt address not an address", func(r *panmailv1.SendEmailRequest) { r.DispositionNotificationTo = "nope" }},
		{"in-reply-to without brackets", func(r *panmailv1.SendEmailRequest) { r.InReplyTo = "a@example.net" }},
		{"in-reply-to with a line break", func(r *panmailv1.SendEmailRequest) { r.InReplyTo = "<a@example.net>\r\nBcc: x@y" }},
		{"in-reply-to without an at", func(r *panmailv1.SendEmailRequest) { r.InReplyTo = "<abc>" }},
		{"message-id too long", func(r *panmailv1.SendEmailRequest) { r.References = []string{long} }},
		{"too many references", func(r *panmailv1.SendEmailRequest) { r.References = tooMany }},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &panmailv1.SendEmailRequest{}
			tc.edit(req)
			var bad *InvalidReplyHeaderError
			if err := validateReplyHeaders(req); !errors.As(err, &bad) {
				t.Fatalf("validateReplyHeaders() = %v, want InvalidReplyHeaderError", err)
			}
		})
	}
}

func TestAttachmentContentIDMustBeOneToken(t *testing.T) {
	for _, id := range []string{"has space", "<bracketed>", "line\r\nbreak", strings.Repeat("a", MaxMessageIDLength+1)} {
		req := &panmailv1.SendEmailRequest{Attachments: []*panmailv1.Attachment{{Filename: "a.png", ContentId: id}}}
		if err := validateAttachments(req); err == nil {
			t.Errorf("content_id %q was accepted", id)
		}
	}
}

func TestReferencesLineFitsOneHeaderLine(t *testing.T) {
	ids := make([]string, MaxReferences)
	for i := range ids {
		ids[i] = "<" + strings.Repeat("x", 40) + string(rune('a'+i%26)) + "@example.net>"
	}
	ids[0] = "<root@example.net>"
	ids[len(ids)-1] = "<newest@example.net>"

	line := referencesLine(ids)
	if len(line) > maxReferencesLineBytes {
		t.Fatalf("line is %d bytes, want at most %d", len(line), maxReferencesLineBytes)
	}
	if !strings.HasPrefix(line, "<root@example.net> ") || !strings.HasSuffix(line, " <newest@example.net>") {
		t.Errorf("line %q does not keep the root and the newest", line)
	}
}
