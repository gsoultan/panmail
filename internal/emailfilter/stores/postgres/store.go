// Package postgres stores filter rules and the queue of what they held.
//
// The SQL is inline rather than embedded from files. Every statement here is
// short and each one is read directly above the function that runs it, which
// for a table this simple is easier to follow than a directory of one-line
// files.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/pkg/cache"
	"github.com/gsoultan/panmail/pkg/db"
)

const (
	// enabledRulesTTL is how long a tenant's compiled rules are reused before
	// they are read again. Writes through this instance take effect at once;
	// this is how stale another instance's edit can be, so it is short.
	enabledRulesTTL = 10 * time.Second

	// enabledRulesMaxEntries bounds the cache: one entry per tenant and
	// direction that has sent or received mail within the TTL.
	enabledRulesMaxEntries = 8192
)

type ruleStore struct {
	conn db.Connection

	// enabled caches Enabled: a tenant's enabled rules for one direction,
	// validated and with their patterns compiled.
	//
	// Enabled runs for every message sent and every message received, and
	// without this each call was a round trip plus a fresh regexp.Compile of
	// every pattern in every rule -- 2,602 allocations and about half a
	// millisecond of CPU for twenty rules of two patterns, on every message,
	// for rules that change a few times a month. The compiled rules are
	// read-only once built, so callers share them.
	enabled *cache.TTLCache[[]emailfilter.Rule]

	// generation is bumped by every write, under mu. A read that started
	// before a write must not cache what it read once the write has
	// invalidated the cache, or the edit would be undone for a TTL.
	mu         sync.Mutex
	generation uint64
}

// NewRuleStore stores the rules a tenant configures.
func NewRuleStore(conn db.Connection) emailfilter.RuleRepository {
	return &ruleStore{
		conn:    conn,
		enabled: cache.NewWithLimit[[]emailfilter.Rule](enabledRulesTTL, enabledRulesMaxEntries),
	}
}

func enabledKey(tenantID string, direction emailfilter.Direction) string {
	return tenantID + "|" + string(direction)
}

// invalidate drops a tenant's cached rules after a write. Both directions,
// because an update can move a rule from one to the other.
func (s *ruleStore) invalidate(tenantID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.enabled.Delete(enabledKey(tenantID, emailfilter.DirectionInbound))
	s.enabled.Delete(enabledKey(tenantID, emailfilter.DirectionOutbound))
}

func (s *ruleStore) getDB() (*sql.DB, error) { return open(s.conn) }

func open(conn db.Connection) (*sql.DB, error) {
	if !conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return conn.GetDB(), nil
}

// ---------------------------------------------------------------- rules

const insertRule = `
INSERT INTO filter_rules
    (id, tenant_id, name, direction, action, priority, enabled, conditions, exceptions, tag, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

func (s *ruleStore) Create(ctx context.Context, r *emailfilter.Rule) error {
	// Validated here as well as at the service, because a rule that reaches
	// the table unvalidated is a rule whose patterns never compile — and a
	// pattern that never compiles is a condition that silently never matches.
	if err := r.Validate(); err != nil {
		return err
	}
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	conditions, exceptions, err := marshalConditions(r)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = conn.ExecContext(ctx, insertRule,
		r.ID, r.TenantID, r.Name, string(r.Direction), string(r.Action),
		r.Priority, r.Enabled, conditions, exceptions, r.Tag, now, now)
	s.invalidate(r.TenantID)
	return err
}

const updateRule = `
UPDATE filter_rules
   SET name = $3, direction = $4, action = $5, priority = $6, enabled = $7,
       conditions = $8, exceptions = $9, tag = $10, updated_at = $11
 WHERE tenant_id = $1 AND id = $2`

func (s *ruleStore) Update(ctx context.Context, r *emailfilter.Rule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	conditions, exceptions, err := marshalConditions(r)
	if err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx, updateRule,
		r.TenantID, r.ID, r.Name, string(r.Direction), string(r.Action),
		r.Priority, r.Enabled, conditions, exceptions, r.Tag, time.Now().UTC())
	s.invalidate(r.TenantID)
	if err != nil {
		return err
	}
	return mustAffectOne(result)
}

func (s *ruleStore) Delete(ctx context.Context, tenantID, id string) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx,
		`DELETE FROM filter_rules WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	s.invalidate(tenantID)
	if err != nil {
		return err
	}
	return mustAffectOne(result)
}

const ruleColumns = `id, tenant_id, name, direction, action, priority, enabled, conditions, exceptions, tag`

func (s *ruleStore) Get(ctx context.Context, tenantID, id string) (*emailfilter.Rule, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT `+ruleColumns+` FROM filter_rules WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	rule, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, emailfilter.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rule, nil
}

func (s *ruleStore) List(ctx context.Context, tenantID string) ([]emailfilter.Rule, error) {
	return s.query(ctx,
		`SELECT `+ruleColumns+` FROM filter_rules WHERE tenant_id = $1 ORDER BY direction, priority, name`,
		tenantID)
}

// Enabled is the read the send path performs on every message, which is why it
// is indexed, why it asks the database for the ordering rather than sorting in
// Go, and why its result is cached -- see ruleStore.enabled.
//
// The returned rules are shared with other callers and must not be modified.
func (s *ruleStore) Enabled(ctx context.Context, tenantID string, direction emailfilter.Direction) ([]emailfilter.Rule, error) {
	key := enabledKey(tenantID, direction)
	if rules, ok := s.enabled.Get(key); ok {
		return rules, nil
	}

	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()

	rules, err := s.query(ctx,
		`SELECT `+ruleColumns+` FROM filter_rules
		  WHERE tenant_id = $1 AND direction = $2 AND enabled = TRUE
		  ORDER BY priority, id`,
		tenantID, string(direction))
	if err != nil {
		return nil, err
	}

	// Compiled here, once per fill, and only here: this is the read whose
	// rules are evaluated. Validate is what compiles the patterns, so a rule
	// handed to the evaluator without it would never match a regex
	// condition. A rule stored invalid fails the read rather than being
	// skipped -- a filter that silently drops a rule fails open.
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			return nil, fmt.Errorf("rule %s is stored invalid: %w", rules[i].ID, err)
		}
	}

	s.mu.Lock()
	if s.generation == generation {
		s.enabled.Put(key, rules)
	}
	s.mu.Unlock()
	return rules, nil
}

func (s *ruleStore) query(ctx context.Context, statement string, args ...any) ([]emailfilter.Rule, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var rules []emailfilter.Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, *rule)
	}
	return rules, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanRule(row scanner) (*emailfilter.Rule, error) {
	var (
		rule                   emailfilter.Rule
		direction, action      string
		conditions, exceptions []byte
		tag                    sql.NullString
	)
	if err := row.Scan(&rule.ID, &rule.TenantID, &rule.Name, &direction, &action,
		&rule.Priority, &rule.Enabled, &conditions, &exceptions, &tag); err != nil {
		return nil, err
	}
	rule.Direction = emailfilter.Direction(direction)
	rule.Action = emailfilter.Action(action)
	rule.Tag = tag.String

	if err := json.Unmarshal(conditions, &rule.Conditions); err != nil {
		return nil, fmt.Errorf("rule %s has unreadable conditions: %w", rule.ID, err)
	}
	if len(exceptions) > 0 {
		if err := json.Unmarshal(exceptions, &rule.Exceptions); err != nil {
			return nil, fmt.Errorf("rule %s has unreadable exceptions: %w", rule.ID, err)
		}
	}
	// Not validated here. Get and List feed the management UI, which shows a
	// rule and never evaluates it, so compiling its patterns would be work
	// thrown away -- and a rule stored invalid has to be listable to be
	// fixed. Enabled, the read that is evaluated, validates and compiles.
	return &rule, nil
}

// marshalConditions encodes the JSON columns as strings, not []byte.
//
// Production connections use pgx's simple protocol, which sends a []byte as a
// bytea literal; PostgreSQL then refuses it for a JSON column, and every rule
// save failed there while passing on SQLite.
func marshalConditions(r *emailfilter.Rule) (conditions string, exceptions sql.NullString, err error) {
	encoded, err := json.Marshal(r.Conditions)
	if err != nil {
		return "", sql.NullString{}, fmt.Errorf("encoding conditions: %w", err)
	}
	if len(r.Exceptions) == 0 {
		return string(encoded), sql.NullString{}, nil
	}
	encodedExceptions, err := json.Marshal(r.Exceptions)
	if err != nil {
		return "", sql.NullString{}, fmt.Errorf("encoding exceptions: %w", err)
	}
	return string(encoded), sql.NullString{String: string(encodedExceptions), Valid: true}, nil
}

func mustAffectOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return emailfilter.ErrNotFound
	}
	return nil
}
