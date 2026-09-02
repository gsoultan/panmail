package emailfilter

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

	// SetRetention changes how long newly held messages wait. Part of the
	// interface because the retention worker pushes to it, the same way it
	// pushes to the outbox and webhook queues.
	SetRetention(d time.Duration)
}

// DefaultQuarantineRetention is how long a held message waits for a reviewer
// before retention gives up on it. Long enough that a queue checked weekly
// still works, short enough that an abandoned quarantine does not become the
// biggest table in the database.
const DefaultQuarantineRetention = 30 * 24 * time.Hour

type screener struct {
	rules      RuleRepository
	quarantine QuarantineRepository
	notifier   HeldNotifier

	// retention is read on the send path and written by the retention worker,
	// so it is guarded. A held message is stamped with whatever the policy was
	// when it was held.
	mu        sync.RWMutex
	retention time.Duration
}

// NewScreener wires the engine to its storage.
//
// The notifier is optional. Without one a hold is silent, which is the
// behaviour to avoid rather than the one to default to — but a deployment with
// no webhook worker must still be able to filter.
func NewScreener(rules RuleRepository, quarantine QuarantineRepository, retention time.Duration, notifier HeldNotifier) Screener {
	if retention <= 0 {
		retention = DefaultQuarantineRetention
	}
	return &screener{rules: rules, quarantine: quarantine, retention: retention, notifier: notifier}
}

// SetRetention changes how long newly held messages wait. Zero means forever,
// as it does for every other retention here.
//
// It applies to messages held from now on and never to ones already waiting.
// Their deadline was stamped when they were held and sent to webhook
// subscribers in the held event; moving it afterwards would break a date this
// gateway already promised. That is the one place quarantine differs from the
// other seven policies, which are applied at prune time and so are retroactive.
//
// The retention worker calls this on every pass, which is what makes a change
// on the settings page apply without a restart.
func (s *screener) SetRetention(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.mu.Lock()
	s.retention = d
	s.mu.Unlock()
}

func (s *screener) currentRetention() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retention
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
		// Zero is forever, so a held message gets no deadline at all rather
		// than one in the past. A quarantine that never expires grows; one
		// that expires by accident loses mail nobody decided about.
		if retention := s.currentRetention(); record.Action == ActionHold && retention > 0 {
			expiry := record.CreatedAt.Add(retention)
			record.ExpiresAt = &expiry
		}
	}
	if err := s.quarantine.Create(ctx, record); err != nil {
		return fmt.Errorf("emailfilter: could not record the decision: %w", err)
	}
	slog.Info("message filtered",
		"id", record.ID, "tenant_id", record.TenantID, "direction", record.Direction,
		"action", record.Action, "rule", record.RuleName)

	// Only a hold is worth waking anyone for. A tag was delivered and a reject
	// is already decided; neither is waiting on a person.
	//
	// After the write, so the notification can never arrive before the message
	// it points at is fetchable. A subscriber that reacts instantly and gets a
	// not-found would be a race this ordering removes rather than documents.
	if record.Action == ActionHold && s.notifier != nil {
		s.notifier.NotifyHeld(record.TenantID, heldEventFor(record))
	}
	return nil
}
