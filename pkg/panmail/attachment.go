package panmail

import (
	"mime"
	"path/filepath"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// Attachment is a file to send with a message.
type Attachment struct {
	// Filename as the recipient sees it. Required.
	Filename string

	// ContentType of the bytes. Left empty it is guessed from the filename's
	// extension, and falls back to application/octet-stream — which every mail
	// client will offer to download rather than display, so set it when you
	// know it.
	ContentType string

	// Content is the raw bytes, not base64: the transport encodes them.
	Content []byte
}

func (a Attachment) proto() *panmailv1.Attachment {
	contentType := a.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(a.Filename))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return &panmailv1.Attachment{
		Filename:    a.Filename,
		ContentType: contentType,
		Content:     a.Content,
	}
}
