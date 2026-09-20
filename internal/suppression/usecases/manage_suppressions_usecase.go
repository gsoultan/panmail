package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrEmptyAddress reports a request that named no mailbox.
var ErrEmptyAddress = errors.New("an email address is required")

// A suppression list only works if the same mailbox always produces the same
// key. "Alice <Alice@Example.COM>" and "alice@example.com" are one mailbox, and
// storing them as two entries means a bounce recorded under one spelling does
// not stop the next send to the other — the list silently fails open, which is
// the one way it must never fail.
//
// gsmail.NormalizeAddress reduces an address to the bare lowercased addr-spec,
// which is exactly the key a suppression list should use. Every write and every
// read goes through here.
// The empty string is refused before it gets there. gsmail v0.9.1's
// NormalizeAddress does not return on an empty input -- it spins -- and every
// entry point below reaches this with whatever a caller sent. Without the
// guard, `AddSuppression{email: ""}` pins a core and never answers, which any
// caller holding suppressions:write can do repeatedly. The guard is here
// rather than at each call site so a new caller cannot reintroduce it.
func suppressionKey(email string) string {
	if strings.TrimSpace(email) == "" {
		return ""
	}
	return gsmail.NormalizeAddress(email)
}

type manageSuppressionsUsecase struct {
	repo stores.SuppressionRepository
}

func NewManageSuppressionsUsecase(repo stores.SuppressionRepository) ManageSuppressionsUsecase {
	return &manageSuppressionsUsecase{
		repo: repo,
	}
}

func (u *manageSuppressionsUsecase) Add(ctx context.Context, tenantID string, req *panmailv1.AddSuppressionRequest) (*panmailv1.Suppression, error) {
	email := suppressionKey(req.Email)
	if email == "" {
		// Storing it would put one unmatchable row per tenant on the list and
		// report success for a suppression that suppresses nothing.
		return nil, ErrEmptyAddress
	}

	s := &entities.Suppression{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     email,
		Reason:    req.Reason,
		CreatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, s); err != nil {
		return nil, err
	}

	return u.toProto(s), nil
}

func (u *manageSuppressionsUsecase) Remove(ctx context.Context, tenantID, email string) error {
	email = suppressionKey(email)
	if email == "" {
		return ErrEmptyAddress
	}
	return u.repo.Delete(ctx, tenantID, email)
}

func (u *manageSuppressionsUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Suppression, string, error) {
	sups, nextToken, err := u.repo.List(ctx, tenantID, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	res := make([]*panmailv1.Suppression, len(sups))
	for i, s := range sups {
		res[i] = u.toProto(s)
	}
	return res, nextToken, nil
}

func (u *manageSuppressionsUsecase) Check(ctx context.Context, tenantID, email string) (bool, string, error) {
	key := suppressionKey(email)
	if key == "" {
		// Nothing is suppressed under no address, and answering without a
		// query keeps an empty request off the database.
		return false, "", nil
	}

	s, err := u.repo.GetByEmail(ctx, tenantID, key)
	if err != nil {
		return false, "", err
	}
	if s == nil {
		return false, "", nil
	}
	return true, s.Reason, nil
}

func (u *manageSuppressionsUsecase) toProto(s *entities.Suppression) *panmailv1.Suppression {
	return &panmailv1.Suppression{
		Id:         s.ID,
		TenantId:   s.TenantID,
		Email:      s.Email,
		Reason:     s.Reason,
		CreateTime: timestamppb.New(s.CreatedAt),
	}
}

// MaxImportEntries bounds one import request.
//
// The list arrives from a caller and is held in memory while it is parsed, so
// it needs a ceiling for the same reason a webhook body does. Ten thousand is
// far above a realistic paste and far below a problem; a larger list is sent
// as several requests, which is safe because importing is idempotent.
const MaxImportEntries = 10000

// maxInvalidSamples bounds how many rejected addresses come back. Enough to
// see the shape of the mistake, not enough to echo a bad file to the caller.
const maxInvalidSamples = 20

// ImportResult accounts for every entry submitted. The three counts sum to the
// number sent, so a caller can tell "nothing to do" from "nothing worked".
type ImportResult struct {
	Imported          int
	AlreadySuppressed int
	Invalid           int
	InvalidSamples    []string
}

// Import adds a suppression list from another provider.
//
// Arriving at a new gateway with an empty list means mailing every address the
// old provider had already learned was dead. That is the single fastest way to
// damage a sending reputation, so moving the list has to be one action rather
// than one call per address.
func (u *manageSuppressionsUsecase) Import(
	ctx context.Context, tenantID string, entries []*panmailv1.SuppressionEntry,
) (ImportResult, error) {
	var result ImportResult

	if len(entries) > MaxImportEntries {
		return result, fmt.Errorf("too many entries: %d, limit is %d", len(entries), MaxImportEntries)
	}

	now := time.Now()
	sups := make([]*entities.Suppression, 0, len(entries))

	// Deduplicated here rather than left to the conflict clause. A file that
	// lists an address twice would otherwise put two rows in one statement,
	// and ON CONFLICT does not see a duplicate inside its own VALUES list --
	// PostgreSQL fails the whole batch with a cardinality violation.
	seen := make(map[string]struct{}, len(entries))

	for _, entry := range entries {
		if entry == nil {
			result.Invalid++
			continue
		}

		// Checked before parsing rather than after: ParseEmailAddress accepts
		// an empty string and hands back an empty address, which reads as a
		// valid entry right up until it is stored as one.
		if strings.TrimSpace(entry.Email) == "" {
			result.Invalid++
			continue
		}

		// ParseEmailAddress rather than a bare syntax check, because an
		// exported list often carries the display-name form,
		// "Alice Smith <alice@example.com>".
		parsed, err := gsmail.ParseEmailAddress(entry.Email)
		if err != nil {
			result.Invalid++
			if len(result.InvalidSamples) < maxInvalidSamples {
				result.InvalidSamples = append(result.InvalidSamples, entry.Email)
			}
			continue
		}

		// The same normalisation Add applies. An imported row stored under a
		// different spelling than the send path looks up is a row that never
		// suppresses anything.
		email := suppressionKey(parsed.Address)
		if email == "" {
			result.Invalid++
			continue
		}
		if _, dup := seen[email]; dup {
			result.AlreadySuppressed++
			continue
		}
		seen[email] = struct{}{}

		reason := strings.TrimSpace(entry.Reason)
		if reason == "" {
			reason = "Imported from a suppression list"
		}

		sups = append(sups, &entities.Suppression{
			ID:        uuid.New().String(),
			TenantID:  tenantID,
			Email:     email,
			Reason:    reason,
			CreatedAt: now,
		})
	}

	inserted, err := u.repo.CreateMany(ctx, sups)
	if err != nil {
		return result, err
	}

	result.Imported = inserted
	// Whatever was sent to the database and did not become a row was already
	// on the list.
	result.AlreadySuppressed += len(sups) - inserted
	return result, nil
}
