// Package mime turns a submitted RFC 5322 message into the request the send
// usecase already understands. It exists so the SMTP transport can stay a
// transport: nothing here talks to a provider, a queue or a database.
package mime

import (
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

// Message is a parsed submission. It carries the routing header separately
// from the send request because the provider is addressing information for
// the gateway, not content, and must never reach the recipient.
type Message struct {
	// ProviderID is the value of the X-Panmail-Provider-Id header, empty when
	// the submitter did not set one. The session falls back to the AUTH
	// username in that case.
	ProviderID string

	// Request is everything the send usecase needs except the provider and
	// the envelope recipients, which the session fills in from the SMTP
	// conversation.
	Request *panmailv1.SendEmailRequest
}
