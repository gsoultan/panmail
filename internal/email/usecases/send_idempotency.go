package usecases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/proto"
)

// An idempotency key makes a retried send safe.
//
// Without one, a client whose connection dropped after the gateway accepted a
// message cannot tell that from one dropped before, and retrying sends the
// message twice. With one, the gateway remembers the key alongside the message
// it accepted and answers a repeat with that message instead of queueing
// another.
//
// What is remembered is acceptance and nothing else. A refusal -- a rate
// limit, a suppressed recipient, an invalid request -- stores nothing, so a
// retry after one is decided afresh: the condition that refused it may have
// cleared, and replaying an old refusal would hold the caller to it.
const (
	// IdempotencyKeyWindow is how long an accepted key is remembered. Long
	// enough to cover any client's retry schedule; short enough that the table
	// stays a day's sends deep.
	IdempotencyKeyWindow = 24 * time.Hour

	// MaxIdempotencyKeyLength bounds a key, in bytes. Keys are printable
	// ASCII, so bytes and characters are the same count.
	MaxIdempotencyKeyLength = 255
)

// ValidateIdempotencyKey reports whether key is usable. Empty is valid and
// means the send has no key.
func ValidateIdempotencyKey(key string) error {
	if len(key) > MaxIdempotencyKeyLength {
		return &InvalidIdempotencyKeyError{Reason: fmt.Sprintf("is %d characters long, the limit is %d", len(key), MaxIdempotencyKeyLength)}
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c > 0x7e {
			return &InvalidIdempotencyKeyError{Reason: "may contain only printable ASCII characters"}
		}
	}
	return nil
}

// InvalidIdempotencyKeyError reports a key that is too long or holds
// something other than printable ASCII. The caller's to fix.
type InvalidIdempotencyKeyError struct {
	Reason string
}

func (e *InvalidIdempotencyKeyError) Error() string {
	return "idempotency key " + e.Reason
}

// IdempotencyKeyConflictError reports a key reused, within its window, for a
// request that is not the one it was first used with. Answering with the
// first message would tell the caller something it did not ask for was sent;
// queueing the second would make the key mean nothing.
type IdempotencyKeyConflictError struct {
	// MessageID is the message the key was first accepted for.
	MessageID string
}

func (e *IdempotencyKeyConflictError) Error() string {
	return "idempotency key reused with a different request"
}

// IdempotencyKeyInFlightError reports a key another request holding it has
// claimed, whose outcome could not be read. It is the rare edge of a race --
// the store said the key was taken and then did not return it -- and the
// answer is to ask again, which will find the winner's message.
type IdempotencyKeyInFlightError struct{}

func (e *IdempotencyKeyInFlightError) Error() string {
	return "a request with this idempotency key is still being processed; retry"
}

// requestHash identifies a request for comparing two uses of one key.
//
// The key itself is left out, so the RPC field and the header produce the same
// hash. Deterministic marshalling, because template_data is a map and an
// ordinary marshal may order it differently each time.
func requestHash(req *panmailv1.SendEmailRequest) (string, error) {
	// Cleared and restored rather than cloned: a clone copies every
	// attachment's bytes to hash them once.
	key := req.IdempotencyKey
	req.IdempotencyKey = ""
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	req.IdempotencyKey = key
	if err != nil {
		return "", fmt.Errorf("failed to hash request: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// replay answers a repeat of an accepted send from what its key recorded.
// (nil, nil) means the key is not live for this tenant and the send proceeds.
func (u *sendEmailUsecase) replay(ctx context.Context, tenantID, key, hash string) (*panmailv1.SendEmailResponse, error) {
	stored, err := u.outboxRepo.GetIdempotencyKey(ctx, tenantID, key, u.now().Add(-IdempotencyKeyWindow))
	if err != nil {
		// Fails closed. Proceeding without knowing whether the key was used is
		// exactly the duplicate the key was sent to prevent.
		return nil, fmt.Errorf("failed to read idempotency key: %w", err)
	}
	if stored == nil {
		return nil, nil
	}
	if stored.RequestHash != hash {
		return nil, &IdempotencyKeyConflictError{MessageID: stored.MessageID}
	}
	// The response the first request got. Accepted is always PENDING, held or
	// not: what has happened to the message since is the events API's to say.
	return &panmailv1.SendEmailResponse{
		MessageId: stored.MessageID,
		Status:    panmailv1.EmailEventType_EMAIL_EVENT_TYPE_PENDING,
	}, nil
}

// lostKeyRace answers a send whose key another request took first.
//
// The store's conflict means the winner's row is committed -- PostgreSQL makes
// the losing insert wait for the winner's transaction, SQLite serialises the
// writers -- so the read finds it. Should it not, the winner's outcome is not
// knowable yet and the caller is told to ask again, which is safe precisely
// because the key is there to make it safe.
func (u *sendEmailUsecase) lostKeyRace(ctx context.Context, tenantID, key, hash string) (*panmailv1.SendEmailResponse, error) {
	res, err := u.replay(ctx, tenantID, key, hash)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, &IdempotencyKeyInFlightError{}
	}
	slog.Info("concurrent send with the same idempotency key answered with the first", "id", res.MessageId, "tenant_id", tenantID)
	return res, nil
}
