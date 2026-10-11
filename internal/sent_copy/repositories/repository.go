// Package repositories stores Sent copies settings, mailbox logins and the
// copies waiting to be filed.
package repositories

import (
	"context"
	"time"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
)

// Repository is everything the Sent copies feature stores.
//
// Implementations encrypt passwords on the way in and decrypt them on the way
// out, so nothing above this interface handles ciphertext. Every read and
// write is scoped to the tenant it is given.
type Repository interface {
	// GetSettings returns the tenant's setting, or nil when none is stored.
	GetSettings(ctx context.Context, tenantID string) (*entities.Settings, error)
	SaveSettings(ctx context.Context, s *entities.Settings) error

	ListMailboxes(ctx context.Context, tenantID string) ([]*entities.Mailbox, error)
	// GetMailbox returns the login for an address, or nil when none is stored.
	GetMailbox(ctx context.Context, tenantID, address string) (*entities.Mailbox, error)
	SaveMailbox(ctx context.Context, m *entities.Mailbox) error
	// DeleteMailbox reports whether a mailbox was removed.
	DeleteMailbox(ctx context.Context, tenantID, address string) (bool, error)

	EnqueueJob(ctx context.Context, job *entities.Job, now time.Time) error
	// ClaimJobs takes up to limit jobs that are due and unclaimed, or whose
	// claim is older than lease, and marks them claimed with a fresh token.
	ClaimJobs(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*entities.Job, error)
	// CompleteJob deletes a job, provided the claim is still the caller's.
	CompleteJob(ctx context.Context, id, claimToken string) error
	// RescheduleJob releases a job to be tried again at next, provided the
	// claim is still the caller's.
	RescheduleJob(ctx context.Context, id, claimToken string, attempts int, next time.Time, lastError string) error
}
