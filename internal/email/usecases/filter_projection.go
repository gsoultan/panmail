package usecases

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
)

// filterAttachments projects the request's attachments onto what a rule can
// ask about. The bytes are deliberately left behind: the evaluator answers
// questions about names, extensions and sizes, and copying a 20 MB attachment
// into a second structure to answer "is it bigger than 10 MB" would put the
// whole thing through memory twice on the send path.
func filterAttachments(in []*panmailv1.Attachment) []emailfilter.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]emailfilter.Attachment, 0, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		out = append(out, emailfilter.Attachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        int64(len(a.Content)),
		})
	}
	return out
}

// approximateSize is what a message-size rule compares against.
//
// Approximate on purpose, and named so. The number a recipient's server sees
// includes MIME boundaries, headers, and base64 expansion that inflates every
// attachment by about a third; computing it exactly would mean building the
// whole MIME message before deciding whether to send it. A rule that says
// "hold anything over 10 MB" wants the order of magnitude, and this is within
// a few percent of it for the case that matters, which is a large attachment.
func approximateSize(subject, html, text string, attachments []*panmailv1.Attachment) int64 {
	size := int64(len(subject) + len(html) + len(text))
	for _, a := range attachments {
		if a == nil {
			continue
		}
		// Base64 is four bytes out for every three in.
		size += int64(len(a.Content))*4/3 + int64(len(a.Filename))
	}
	return size
}
