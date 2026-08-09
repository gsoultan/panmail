package worker

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
)

// inboundNamespace keeps derived identifiers from colliding with anything else
// that happens to be a UUID.
var inboundNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("panmail/inbound"))

// messageIdentity derives a stable id for a received message.
//
// The poller re-reads the most recent messages every tick, so an identifier
// has to be a property of the message rather than of the moment it was read.
// It used to be uuid.New(): a fresh value on every pass, which meant one
// arriving email was stored again every interval, and each copy re-ran bounce
// detection and fired the tenant's webhooks again. At a thirty-second interval
// that is a hundred and twenty copies an hour of a message that arrived once.
//
// Message-ID first: it is assigned by the sending system, required by RFC 5322,
// globally unique, and stable no matter how often or from where the mailbox is
// read. UID is the fallback — unique only within a mailbox, and reset by a
// UIDVALIDITY change — so it is scoped by provider to keep two mailboxes from
// colliding on the same number.
//
// A message with neither is given a content-derived identity. That is weaker:
// two genuinely identical messages collapse into one. It is still the better
// failure, because the alternative is storing the same message forever.
func messageIdentity(tenantID, providerID string, e gsmail.Email) string {
	if id := headerValue(e.Headers, "Message-ID"); id != "" {
		return derive(tenantID, "msgid", id)
	}

	if e.UID != 0 {
		return derive(tenantID, "uid", fmt.Sprintf("%s/%s/%d", providerID, e.Mailbox, e.UID))
	}

	return derive(tenantID, "content", strings.Join([]string{
		e.From, strings.Join(e.To, ","), e.Subject, string(e.Body),
	}, "\x00"))
}

func derive(tenantID, kind, value string) string {
	// Scoped by tenant so two tenants receiving the same broadcast — same
	// Message-ID, legitimately — do not overwrite each other's copy.
	return uuid.NewSHA1(inboundNamespace, []byte(tenantID+"\x00"+kind+"\x00"+value)).String()
}

// headerValue reads a header case-insensitively, since IMAP servers disagree
// about the capitalisation of Message-ID.
func headerValue(headers map[string]string, name string) string {
	if headers == nil {
		return ""
	}
	if v, ok := headers[name]; ok {
		return strings.TrimSpace(v)
	}
	lower := strings.ToLower(name)
	for k, v := range headers {
		if strings.ToLower(k) == lower {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// receivedAt prefers the message's own Date header over the moment it was
// read.
//
// Using time.Now() made the timestamp a property of when the poller happened
// to run, so a mailbox read after an outage showed a week of mail as having
// arrived at once, in the wrong order.
func receivedAt(e gsmail.Email) time.Time {
	if raw := headerValue(e.Headers, "Date"); raw != "" {
		if t, err := mail.ParseDate(raw); err == nil {
			return t
		}
	}
	return time.Now()
}
