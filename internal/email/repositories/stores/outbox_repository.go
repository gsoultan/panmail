package stores

import (
	"context"
	"errors"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

type OutboxRepository interface {
	Create(ctx context.Context, email *entities.OutboxEmail) error

	// CreateWithIdempotencyKey queues email and records key against it in one
	// transaction, so a key is never stored for a message that was not queued
	// and a queued message is never missing its key. A key that is already
	// live for the tenant -- one created at or after liveSince -- queues
	// nothing and returns ErrIdempotencyKeyTaken; an expired one is replaced.
	CreateWithIdempotencyKey(ctx context.Context, email *entities.OutboxEmail, key *entities.IdempotencyKey, liveSince time.Time) error

	// GetIdempotencyKey returns the tenant's use of key created at or after
	// liveSince, or (nil, nil) when there is none.
	GetIdempotencyKey(ctx context.Context, tenantID, key string, liveSince time.Time) (*entities.IdempotencyKey, error)

	// PruneIdempotencyKeys removes keys created before the cutoff, and reports
	// how many went.
	PruneIdempotencyKeys(ctx context.Context, olderThan time.Time) (int64, error)

	// GetByID returns (nil, nil) when there is no such message. A row that has
	// gone is an answer, not a failure: a delivered or discarded message is
	// deleted, and a caller retrying an operation has to be able to tell that
	// apart from a database it could not reach.
	GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error)

	// ClaimPending atomically takes ownership of up to limit due messages for
	// leaseFor, and returns them. Two processes polling the same table must
	// never both receive the same message, or the recipient gets it twice.
	//
	// A lease that expires without the message being resolved is reclaimable,
	// so a worker that dies mid-send does not strand mail.
	ClaimPending(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.OutboxEmail, error)

	// RenewClaim extends a claim's lease to until, and reports whether the
	// claim is still the caller's. False means the lease lapsed and another
	// worker claimed the message since, or it has been settled: either way it
	// is no longer this caller's to send.
	RenewClaim(ctx context.Context, id, claimToken string, until time.Time) (bool, error)

	Update(ctx context.Context, email *entities.OutboxEmail) error
	Delete(ctx context.Context, id string) error
	CountPending(ctx context.Context, tenantID string) (int64, error)

	// PruneTerminal removes messages in a terminal state last touched before
	// the cutoff, and reports how many went.
	//
	// Of what the worker settles, only failures accumulate — a delivered
	// message is deleted outright — but they accumulate forever, and each row carries the whole serialised
	// request including the body. Over the life of a deployment that is the
	// largest table in the database, holding nothing anyone will read.
	PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error)

	// PruneHeld removes held messages created before the cutoff whose review
	// is no longer pending, and reports how many went. It is the backstop for
	// a held payload nothing discarded; a message a reviewer can still release
	// is never removed, however old.
	PruneHeld(ctx context.Context, olderThan time.Time) (int64, error)

	// Stats reports how much is queued and how old the oldest of it is.
	//
	// The age is the part worth alerting on: a depth of five hundred is a busy
	// minute or a stopped worker and cannot tell you which.
	Stats(ctx context.Context) (pending int64, oldest time.Time, err error)
}

// ErrIdempotencyKeyTaken is CreateWithIdempotencyKey losing to a live use of
// the same key: another request holding it was accepted first, and nothing was
// queued for this one.
var ErrIdempotencyKeyTaken = errors.New("idempotency key already in use")
