package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
)

// claimLease is how long a post may hold a message's claim before another post
// of it may take over. Far longer than processing one message takes, so a slow
// post is not overtaken by its own retry; short enough that a post which died
// mid-way does not lose the message for longer than a provider's retry
// schedule.
const claimLease = 5 * time.Minute

// inFlightRetryAfter is what a provider is told when another post of the same
// message is still being processed.
const inFlightRetryAfter = "30"

// receiptNamespace keeps ids derived from a dedupe key from colliding with
// anything else that happens to be a UUID.
var receiptNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("panmail/inbound/http"))

// dedupeKey returns what identifies the message a post carries, hashed, or ""
// when nothing does.
//
// The provider's own event id comes first: it is what the provider puts on
// every retry of one post, and nothing else is as exact. Failing that, the
// Message-ID together with a digest of the parsed message, because a
// Message-ID is chosen by whoever sent the mail and is not to be trusted on
// its own to tell two messages apart. The digest is of the parsed fields and
// not of the raw body: a multipart retry is built afresh with a new boundary,
// so its bytes differ even when the message does not.
//
// A post with neither is not deduplicated. A message with no distinguishing
// property cannot be recognised when it is posted again, and keying on the
// content alone would merge two genuinely separate messages that happen to say
// the same thing.
func dedupeKey(p *inboundPayload) string {
	if p.EventID != "" {
		return digest("event", p.EventID)
	}
	if id := p.messageID(); id != "" {
		return digest("message-id", id,
			p.From, strings.Join(p.To, "\n"), p.Subject, p.Text, p.HTML)
	}
	return ""
}

// digest hashes its parts with their lengths, so no two different lists of
// parts can produce the same input to the hash.
func digest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// messageID returns the Message-ID the post carries, from its own field or
// from its headers.
func (p *inboundPayload) messageID() string {
	if id := strings.TrimSpace(p.MessageID); id != "" {
		return id
	}
	for name, value := range p.Headers {
		if strings.EqualFold(name, "Message-ID") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// inboundID derives the stored message's id from its dedupe key, so a retry
// that does get past the receipt -- because the receipt store could not be
// reached -- is still caught by the inbound store's own check for an id it
// already holds.
func inboundID(tenantID, providerID, key string) string {
	if key == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(receiptNamespace, []byte(tenantID+"\x00"+providerID+"\x00"+key)).String()
}

// receipt is one post's hold on a message's claim. The zero value holds
// nothing, and settling it does nothing.
type receipt struct {
	store                     stores.ReceiptRepository
	tenantID, providerID, key string
	token                     string
}

// claim asks the receipt store whether this post is the one to process the
// message.
//
// A store that cannot answer is treated as a claim acquired. Refusing mail
// because the database was briefly unreachable would lose it if the provider
// gave up, whereas processing a retry twice is what happened before receipts
// existed; and the derived id still catches a retry on the same instance.
func (h *WebhookHandler) claim(ctx context.Context, tenantID, providerID, key string) (stores.ClaimResult, receipt) {
	if h.receipts == nil || key == "" {
		return stores.ClaimAcquired, receipt{}
	}
	result, token, err := h.receipts.Claim(ctx, tenantID, providerID, key, claimLease)
	if err != nil {
		slog.Error("could not record an inbound receipt; processing without deduplication",
			"error", err, "tenant_id", tenantID, "provider_id", providerID)
		return stores.ClaimAcquired, receipt{}
	}
	if result != stores.ClaimAcquired {
		return result, receipt{}
	}
	return result, receipt{store: h.receipts, tenantID: tenantID, providerID: providerID, key: key, token: token}
}

// complete marks the message committed. A failure is logged and nothing more:
// the message is stored, and the worst a stuck claim costs is that a retry
// after the lease is processed again.
func (r receipt) complete(ctx context.Context) {
	if r.store == nil {
		return
	}
	if err := r.store.Complete(context.WithoutCancel(ctx), r.tenantID, r.providerID, r.key, r.token); err != nil {
		slog.Error("could not complete an inbound receipt",
			"error", err, "tenant_id", r.tenantID, "provider_id", r.providerID)
	}
}

// release gives the claim up after a failure, so the provider's retry is
// processed rather than answered as a duplicate of a message never stored. A
// release that fails leaves the claim to expire with its lease.
func (r receipt) release(ctx context.Context) {
	if r.store == nil {
		return
	}
	if err := r.store.Release(context.WithoutCancel(ctx), r.tenantID, r.providerID, r.key, r.token); err != nil {
		slog.Error("could not release an inbound receipt",
			"error", err, "tenant_id", r.tenantID, "provider_id", r.providerID)
	}
}
