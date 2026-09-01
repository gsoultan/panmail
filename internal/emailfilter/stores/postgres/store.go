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
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/pkg/db"
)

type ruleStore struct {
	conn db.Connection
}

// NewRuleStore stores the rules a tenant configures.
func NewRuleStore(conn db.Connection) emailfilter.RuleRepository {
	return &ruleStore{conn: conn}
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
// is indexed and why it asks the database for the ordering rather than sorting
// in Go.
func (s *ruleStore) Enabled(ctx context.Context, tenantID string, direction emailfilter.Direction) ([]emailfilter.Rule, error) {
	return s.query(ctx,
		`SELECT `+ruleColumns+` FROM filter_rules
		  WHERE tenant_id = $1 AND direction = $2 AND enabled = TRUE
		  ORDER BY priority, id`,
		tenantID, string(direction))
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
	// Validated on the way out, not just on the way in. Validate is what
	// compiles the patterns, so a rule read from the table and handed straight
	// to the evaluator without this would never match a regex condition.
	if err := rule.Validate(); err != nil {
		return nil, fmt.Errorf("rule %s is stored invalid: %w", rule.ID, err)
	}
	return &rule, nil
}

func marshalConditions(r *emailfilter.Rule) (conditions, exceptions []byte, err error) {
	if conditions, err = json.Marshal(r.Conditions); err != nil {
		return nil, nil, fmt.Errorf("encoding conditions: %w", err)
	}
	if len(r.Exceptions) == 0 {
		return conditions, nil, nil
	}
	if exceptions, err = json.Marshal(r.Exceptions); err != nil {
		return nil, nil, fmt.Errorf("encoding exceptions: %w", err)
	}
	return conditions, exceptions, nil
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
