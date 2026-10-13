package emailfilter

import (
	"errors"
	"log/slog"

	"github.com/gsoultan/panmail/internal/emailfilter/attachtext"
)

// needsAttachmentText reports whether any rule asks about the text inside an
// attachment, in a condition or an exception. A sensitive-data condition
// counts: a card number in payroll.xlsx is the case it exists for.
func needsAttachmentText(rules []Rule) bool {
	for _, r := range rules {
		for _, list := range [][]Condition{r.Conditions, r.Exceptions} {
			for _, c := range list {
				if c.Field == FieldAttachmentText || c.Field == FieldSensitiveData {
					return true
				}
			}
		}
	}
	return false
}

// readAttachmentText fills in Text for every attachment it can read.
//
// The slice is copied before it is written to, so the caller's Message keeps
// its own attachments. A file that cannot be read -- encrypted, malformed,
// over the size limit -- keeps an empty Text and is logged: a rule about
// content cannot match content nobody can read, and refusing the message over
// it would make an unreadable attachment a way to block a tenant's mail.
func readAttachmentText(tenantID string, m Message) Message {
	if len(m.Attachments) == 0 {
		return m
	}
	attachments := make([]Attachment, len(m.Attachments))
	copy(attachments, m.Attachments)

	for i := range attachments {
		a := &attachments[i]
		if a.Text != "" || len(a.Data) == 0 || !attachtext.Supported(a.Filename, a.ContentType) {
			continue
		}
		text, err := attachtext.Extract(a.Filename, a.ContentType, a.Data)
		if err != nil && !errors.Is(err, attachtext.ErrUnsupported) {
			slog.Warn("could not read the text of an attachment for filtering",
				"tenant_id", tenantID, "filename", a.Filename, "error", err)
		}
		a.Text = text
	}
	m.Attachments = attachments
	return m
}
