package stores

import (
	"context"
	"time"
)

// ClaimResult is what a post to the HTTP inbound endpoint learns about the
// message it carries.
type ClaimResult int

const (
	// ClaimAcquired: this post is the one to process the message. It must
	// call Complete once the message is committed, or Release if it is not.
	ClaimAcquired ClaimResult = iota + 1

	// ClaimDuplicate: the message was committed by an earlier post. The
	// provider is retrying something panmail already has.
	ClaimDuplicate

	// ClaimInFlight: another post of the same message holds the claim and has
	// not finished. Answering success here would be a lie if that post then
	// failed, so the caller asks the provider to retry later instead.
	ClaimInFlight
)

// ReceiptRepository records which posts to the HTTP inbound endpoint have been
// accepted, so a provider's retry is recognised wherever it lands.
//
// Keys are scoped by tenant and provider and are opaque to the store: the
// caller hashes whatever identifies the message.
type ReceiptRepository interface {
	// Claim records that a post of the message keyed by key is being
	// processed. A claim older than lease belongs to a post that died, and is
	// taken over. The token returned with ClaimAcquired is what Complete and
	// Release must present, so a post whose claim was taken over cannot
	// settle the claim that replaced it.
	Claim(ctx context.Context, tenantID, providerID, key string, lease time.Duration) (ClaimResult, string, error)

	// Complete marks the message committed. Later posts of it are duplicates.
	Complete(ctx context.Context, tenantID, providerID, key, token string) error

	// Release gives up a claim whose message was not committed, so the
	// provider's retry is processed rather than taken for a duplicate.
	Release(ctx context.Context, tenantID, providerID, key, token string) error

	// PruneBefore removes receipts created before the cutoff.
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}
