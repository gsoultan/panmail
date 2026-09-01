package emailfilter

import (
	"fmt"
	"sort"
	"strings"
)

// Action is what happens to a message a rule matched.
type Action string

const (
	// ActionHold quarantines the message for review. It is not delivered and
	// not lost: it waits in the review queue until someone releases or rejects
	// it.
	ActionHold Action = "hold"

	// ActionReject refuses the message outright. Outbound the caller is told
	// so while it is still listening, which is the whole point — a rejection
	// nobody sees is a message that silently vanished.
	ActionReject Action = "reject"

	// ActionTag delivers the message and records the match, for a rule you
	// want to watch before you trust it enough to hold on.
	ActionTag Action = "tag"

	// ActionAllow delivers the message and stops evaluation, so a narrow
	// exemption can sit above a broad rule rather than being written as an
	// exception on every one of them.
	ActionAllow Action = "allow"
)

func (a Action) Valid() bool {
	switch a {
	case ActionHold, ActionReject, ActionTag, ActionAllow:
		return true
	}
	return false
}

// Rule is a set of conditions and an action.
//
// Conditions are ANDed and exceptions are ANDed-not: the rule fires when every
// condition holds and no exception does. Values inside a single condition are
// ORed. That is Exchange's model, and it is worth copying because it expresses
// what people actually write without needing a nested expression — and a form
// for a nested expression is a form nobody fills in correctly.
type Rule struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Direction Direction `json:"direction"`
	Action    Action    `json:"action"`

	// Priority orders evaluation, lowest first. The first rule that matches
	// decides; nothing after it runs.
	Priority int `json:"priority"`

	Enabled bool `json:"enabled"`

	Conditions []Condition `json:"conditions"`
	Exceptions []Condition `json:"exceptions,omitempty"`

	// Tag is the label recorded when Action is ActionTag.
	Tag string `json:"tag,omitempty"`
}

// Validate checks the rule and compiles its patterns. A rule that has not been
// validated will never match a pattern condition, because compiling is what
// populates it — so this is not optional, and the store calls it on read as
// well as on write.
func (r *Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("emailfilter: a rule needs a name")
	}
	if !r.Direction.Valid() {
		return fmt.Errorf("emailfilter: %q is not a direction", r.Direction)
	}
	if !r.Action.Valid() {
		return fmt.Errorf("emailfilter: %q is not an action", r.Action)
	}
	// A rule with no conditions matches every message. Exchange allows it and
	// calls it "apply to all messages"; here it would mean one careless save
	// quarantines a tenant's entire outbound mail, so it is refused.
	if len(r.Conditions) == 0 {
		return fmt.Errorf("emailfilter: a rule needs at least one condition")
	}
	for i := range r.Conditions {
		if err := r.Conditions[i].Validate(r.Direction); err != nil {
			return fmt.Errorf("condition %d: %w", i+1, err)
		}
	}
	for i := range r.Exceptions {
		if err := r.Exceptions[i].Validate(r.Direction); err != nil {
			return fmt.Errorf("exception %d: %w", i+1, err)
		}
	}
	return nil
}

// Matches reports whether the rule fires, and which conditions carried it.
// The matched list is what the review queue shows: "held by rule X" is not
// reviewable, "held because the subject contained 'wire transfer' and there
// was a .exe attached" is.
func (r Rule) Matches(m Message) ([]Condition, bool) {
	if !r.Enabled {
		return nil, false
	}
	matched := make([]Condition, 0, len(r.Conditions))
	for _, condition := range r.Conditions {
		if !condition.Matches(m) {
			return nil, false
		}
		matched = append(matched, condition)
	}
	for _, exception := range r.Exceptions {
		if exception.Matches(m) {
			return nil, false
		}
	}
	return matched, true
}

// Decision is what the pipeline should do with a message.
type Decision struct {
	// Action is empty when nothing matched, or when an allow rule matched:
	// both mean deliver normally. Callers should switch on it rather than on
	// whether Rule is nil.
	Action Action

	// Rule is the rule that decided, including an allow rule. Nil when nothing
	// matched at all.
	Rule *Rule

	// Matched is the conditions that fired, for the audit trail.
	Matched []Condition
}

// Delivers reports whether the message should carry on through the pipeline.
// Tagged messages deliver; held and rejected ones do not.
func (d Decision) Delivers() bool {
	return d.Action == "" || d.Action == ActionAllow || d.Action == ActionTag
}

// Evaluate runs the rules against a message and returns the first decision.
//
// Rules are sorted by priority and the first match wins, so an allow rule
// placed above a hold rule exempts what it matches. Rules for the other
// direction, and disabled ones, are skipped.
//
// A nil or empty rule set returns the zero Decision, which delivers — filtering
// that is not configured must not be filtering that blocks.
func Evaluate(rules []Rule, direction Direction, m Message) Decision {
	applicable := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.Direction == direction && rule.Enabled {
			applicable = append(applicable, rule)
		}
	}
	// Stable, so two rules sharing a priority keep the order the store
	// returned rather than shuffling between evaluations.
	sort.SliceStable(applicable, func(i, j int) bool {
		return applicable[i].Priority < applicable[j].Priority
	})

	for i := range applicable {
		matched, ok := applicable[i].Matches(m)
		if !ok {
			continue
		}
		rule := applicable[i]
		if rule.Action == ActionAllow {
			return Decision{Rule: &rule, Matched: matched}
		}
		return Decision{Action: rule.Action, Rule: &rule, Matched: matched}
	}
	return Decision{}
}
