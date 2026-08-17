package panmail

import (
	"errors"
	"fmt"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Message is the mail to send.
//
// Either a body (HTML, Text, or both) or a TemplateID is required — a message
// with neither has nothing to say, and the gateway refuses it.
type Message struct {
	// ProviderID is the configured sending provider, as shown on the Email
	// Providers page. Required: panmail will not guess which of a tenant's
	// providers a message should go out through, because the wrong guess is a
	// message sent from the wrong domain.
	ProviderID string

	// From must be an address the provider is authorised to send as.
	From string

	To  []string
	Cc  []string
	Bcc []string

	Subject string

	// HTML and Text are the two bodies. Send both when you can: a text part is
	// what recipients with images off, screen readers and spam filters read.
	HTML string
	Text string

	// TemplateID renders a stored template with TemplateData instead of using
	// the bodies above. Subject comes from the template unless set here.
	TemplateID   string
	TemplateData map[string]any

	Attachments []Attachment
}

// ErrNoRecipients reports a message addressed to nobody.
var ErrNoRecipients = errors.New("panmail: at least one recipient is required")

// request validates the message and converts it to the wire type.
//
// Validated here as well as on the server so that the obvious mistakes cost a
// clear error rather than a round trip and a rejection phrased in the
// gateway's terms.
func (m Message) request() (*panmailv1.SendEmailRequest, error) {
	if m.ProviderID == "" {
		return nil, errors.New("panmail: provider id is required")
	}
	if m.From == "" {
		return nil, errors.New("panmail: from address is required")
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return nil, ErrNoRecipients
	}
	if m.HTML == "" && m.Text == "" && m.TemplateID == "" {
		return nil, errors.New("panmail: a message needs an html body, a text body or a template id")
	}

	req := &panmailv1.SendEmailRequest{
		ProviderId: m.ProviderID,
		From:       m.From,
		To:         m.To,
		Cc:         m.Cc,
		Bcc:        m.Bcc,
		Subject:    m.Subject,
		BodyHtml:   m.HTML,
		BodyText:   m.Text,
		TemplateId: m.TemplateID,
	}

	if len(m.TemplateData) > 0 {
		data, err := structpb.NewStruct(m.TemplateData)
		if err != nil {
			// Reported against the template data specifically: the underlying
			// error names the offending value but not where it came from, and
			// "invalid type" on its own sends people looking in the wrong place.
			return nil, fmt.Errorf("panmail: template data is not representable as JSON: %w", err)
		}
		req.TemplateData = data
	}

	for _, attachment := range m.Attachments {
		if attachment.Filename == "" {
			return nil, errors.New("panmail: every attachment needs a filename")
		}
		req.Attachments = append(req.Attachments, attachment.proto())
	}

	return req, nil
}
