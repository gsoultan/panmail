package retention

import (
	"context"
	"time"
)

// InboundPruner removes received mail stored before a cutoff.
//
// Identical in shape to LogPruner and deliberately a separate type: these two
// hold the least and the most valuable data panmail keeps, and a policy that
// can be wired to the wrong one by accident is a policy that deletes a
// tenant's mail when it was asked to tidy up debug logs.
type InboundPruner interface {
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)
}

// InboundReceiptPruner removes the inbound endpoint's records of posts already
// accepted. They hold hashes, not mail, so they are pruned on a fixed horizon
// rather than on the inbound policy, which defaults to keeping forever.
type InboundReceiptPruner interface {
	PruneBefore(ctx context.Context, before time.Time) (int64, error)
}

// InboundReceiptDays keeps a week of inbound receipts. A receipt only has to
// outlive the provider's retries of one post: SendGrid's run for three days,
// and Mailgun's, Postmark's and SNS's default policy for hours. A retry after
// the receipt is gone is stored again, which is what every retry did before
// receipts existed.
const InboundReceiptDays = 7
