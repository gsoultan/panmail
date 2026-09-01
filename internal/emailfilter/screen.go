package emailfilter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Screener decides what a pipeline should do with a message, and records the
// decision when it is one worth reviewing.
//
// Both pipelines hold it as an interface and both tolerate a nil one, because
// filtering is a feature a tenant may never turn on and a gateway with no
// rules configured must send mail exactly as it did before.
type Screener interface {
	Screen(ctx context.Context, tenantID string, direction Direction, m Message) (Decision, error)
	Record(ctx context.Context, record *FilteredMessage) error
}

// DefaultQuarantineRetention is how long a held message waits for a reviewer
// before retention gives up on it. Long enough that a queue checked weekly
// still works, short enough that an abandoned quarantine does not become the
// biggest table in the database.
const DefaultQuarantineRetention = 30 * 24 * time.Hour

type screener struct {
	rules      RuleRepository
	quarantine QuarantineRepository
	retention  time.Duration
}

// NewScreener wires the engine to its storage.
func NewScreener(rules RuleRepository, quarantine QuarantineRepository, retention time.Duration) Screener {
	if retention <= 0 {
		retention = DefaultQuarantineRetention
	}
	return &screener{rules: rules, quarantine: quarantine, retention: retention}
}

// Screen evaluates a tenant's rules against a message.
//
// A failure to read the rules is reported rather than swallowed, and the
// caller decides. That is deliberate: filtering exists to stop things, and a
// gateway that silently sends everything whenever its database is briefly
// unreachable is a filter that fails open at exactly the moment it matters.
// The send path turns this into a refusal the caller can retry.
func (s *screener) Screen(ctx context.Context, tenantID string, direction Direction, m Message) (Decision, error) {
	rules, err := s.rules.Enabled(ctx, tenantID, direction)
	if err != nil {
		return Decision{}, fmt.Errorf("emailfilter: could not read rules: %w", err)
	}
	if len(rules) == 0 {
		return Decision{}, nil
	}
	return Evaluate(rules, direction, m), nil
}

// Record writes a decision to the review queue, filling in the identity and
// expiry the caller should not have to think about.
func (s *screener) Record(ctx context.Context, record *FilteredMessage) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.ExpiresAt == nil {
		// Only a held message expires. A rejected one is already terminal and
		// a tagged one was delivered; both are history, and history is pruned
		// by retention on its own schedule rather than by this clock.
		if record.Action == ActionHold {
			expiry := record.CreatedAt.Add(s.retention)
			record.ExpiresAt = &expiry
		}
	}
	if err := s.quarantine.Create(ctx, record); err != nil {
		return fmt.Errorf("emailfilter: could not record the decision: %w", err)
	}
	slog.Info("message filtered",
		"id", record.ID, "tenant_id", record.TenantID, "direction", record.Direction,
		"action", record.Action, "rule", record.RuleName)
	return nil
}
