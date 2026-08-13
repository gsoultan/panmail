package worker

import (
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
)

// inboundNamespace keeps derived identifiers from colliding with anything else
// that happens to be a UUID.
var inboundNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("panmail/inbound"))

// messageIdentity derives a stable id for a received message.
//
// The poller re-reads the most recent messages every tick, so an identifier has
// to be a property of the message rather than of the moment it was read. It
// used to be uuid.New(): a fresh value on every pass, which meant one arriving
// email was stored again every interval, and each copy re-ran bounce detection
// and fired the tenant's webhooks again.
//
// The precedence — Message-ID, then mailbox and UID, then a content digest — is
// gsmail's, since it is a fact about email rather than about this service.
// gsmail also reports which of the three it used, and says to key on the pair:
// the three produce different kinds of value and nothing stops one colliding
// with another.
//
// What belongs here is the scoping, which is the part that is about panmail.
func messageIdentity(tenantID, providerID string, e gsmail.Email) string {
	id, source := e.MessageIdentity()
	if id == "" {
		// Nothing to identify it by. Returning empty rather than inventing
		// something lets Process store it without deduplication, which is the
		// honest outcome: a message with no distinguishing property cannot be
		// recognised on the next pass.
		return ""
	}

	switch source {
	case gsmail.IdentityUID:
		// A UID means something only within one mailbox on one server, so two
		// providers using the same number are different messages.
		return derive(tenantID, string(source), providerID+"/"+id)
	default:
		// A Message-ID identifies the message anywhere it is found, so it is
		// deliberately not scoped by provider: the same message read from two
		// of a tenant's accounts is one message, and should be stored once.
		return derive(tenantID, string(source), id)
	}
}

func derive(tenantID, kind, value string) string {
	// Scoped by tenant so two tenants receiving the same broadcast — same
	// Message-ID, legitimately — do not overwrite each other's copy. The source
	// is part of the key because a digest and a Message-ID are different kinds
	// of value that nothing otherwise keeps apart.
	return uuid.NewSHA1(inboundNamespace, []byte(tenantID+"\x00"+kind+"\x00"+value)).String()
}

// receivedAt prefers the message's own Date header over the moment it was read.
//
// Using time.Now() made the timestamp a property of when the poller happened to
// run, so a mailbox read after an outage showed a week of mail as having
// arrived at once, in the wrong order.
func receivedAt(e gsmail.Email) time.Time {
	// gsmail's Header reads case-insensitively, which matters because a parsed
	// message carries whatever spelling net/mail produced rather than the one
	// the sender wrote.
	if raw := e.Header("Date"); raw != "" {
		if t, err := mail.ParseDate(raw); err == nil {
			return t
		}
	}
	return time.Now()
}
