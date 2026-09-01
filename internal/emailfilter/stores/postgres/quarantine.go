package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/pkg/db"
)

type quarantineStore struct {
	conn db.Connection
}

// NewQuarantineStore stores decisions taken.
func NewQuarantineStore(conn db.Connection) emailfilter.QuarantineRepository {
	return &quarantineStore{conn: conn}
}

func (s *quarantineStore) getDB() (*sql.DB, error) { return open(s.conn) }

const insertFiltered = `
INSERT INTO filtered_messages
    (id, tenant_id, direction, rule_id, rule_name, action, status, message_id, provider_id,
     from_address, recipients, subject, size_bytes, attachment_count, attachment_names,
     matched, payload_ref, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`

func (s *quarantineStore) Create(ctx context.Context, m *emailfilter.FilteredMessage) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	recipients, err := json.Marshal(m.Recipients)
	if err != nil {
		return fmt.Errorf("encoding recipients: %w", err)
	}
	names, err := json.Marshal(m.AttachmentNames)
	if err != nil {
		return fmt.Errorf("encoding attachment names: %w", err)
	}
	matched, err := json.Marshal(m.Matched)
	if err != nil {
		return fmt.Errorf("encoding matched conditions: %w", err)
	}
	if m.Status == "" {
		m.Status = emailfilter.StatusPending
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}

	_, err = conn.ExecContext(ctx, insertFiltered,
		m.ID, m.TenantID, string(m.Direction), nullable(m.RuleID), m.RuleName,
		string(m.Action), string(m.Status), nullable(m.MessageID), nullable(m.ProviderID),
		m.From, recipients, m.Subject, m.SizeBytes, m.AttachmentCount, names,
		matched, nullable(m.PayloadRef), m.CreatedAt, m.ExpiresAt)
	return err
}

const filteredColumns = `
    id, tenant_id, direction, rule_id, rule_name, action, status, message_id, provider_id,
    from_address, recipients, subject, size_bytes, attachment_count, attachment_names,
    matched, payload_ref, reviewed_by, reviewed_at, review_note, created_at, expires_at`

func (s *quarantineStore) Get(ctx context.Context, tenantID, id string) (*emailfilter.FilteredMessage, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT `+filteredColumns+` FROM filtered_messages WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	m, err := scanFiltered(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, emailfilter.ErrNotFound
	}
	return m, err
}

// defaultPageSize and maxPageSize bound a listing. A review queue is unbounded
// by nature — it grows for exactly as long as nobody looks at it — so a caller
// that asks for everything must not get everything.
const (
	defaultPageSize = 50
	maxPageSize     = 500
)

func (s *quarantineStore) List(ctx context.Context, tenantID string, f emailfilter.QuarantineFilter) ([]emailfilter.FilteredMessage, string, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	size := f.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	if f.Direction != "" {
		args = append(args, string(f.Direction))
		where = append(where, "direction = $"+strconv.Itoa(len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	// Keyset rather than OFFSET: the queue is written to while it is being
	// read, and an offset page would skip or repeat rows as rows arrive.
	if f.PageToken != "" {
		args = append(args, f.PageToken)
		where = append(where, "id > $"+strconv.Itoa(len(args)))
	}
	args = append(args, size+1)

	statement := `SELECT ` + filteredColumns + ` FROM filtered_messages WHERE ` +
		joinAnd(where) + ` ORDER BY id LIMIT $` + strconv.Itoa(len(args))

	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	out := make([]emailfilter.FilteredMessage, 0, size)
	for rows.Next() {
		m, err := scanFiltered(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	// One more than asked for is how we know there is a next page without a
	// second count query.
	var next string
	if len(out) > size {
		next = out[size-1].ID
		out = out[:size]
	}
	return out, next, nil
}

// reviewFiltered moves a message out of PENDING, and only out of PENDING.
//
// The status check is in the WHERE clause rather than in a read before the
// write. Two reviewers pressing release at the same moment is not a rare race:
// it is what happens when a queue is worked by more than one person, and the
// losing update has to affect zero rows rather than send the mail a second
// time.
const reviewFiltered = `
UPDATE filtered_messages
   SET status = $3, reviewed_by = $4, reviewed_at = $5, review_note = $6
 WHERE tenant_id = $1 AND id = $2 AND status = 'PENDING'`

func (s *quarantineStore) Review(ctx context.Context, tenantID, id string, status emailfilter.Status, reviewedBy, note string) (*emailfilter.FilteredMessage, error) {
	if !status.Terminal() {
		return nil, fmt.Errorf("emailfilter: %q is not a review outcome", status)
	}
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	result, err := conn.ExecContext(ctx, reviewFiltered,
		tenantID, id, string(status), reviewedBy, time.Now().UTC(), note)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		// Either it does not exist, or somebody else already decided. Telling
		// the two apart costs a read and matters to the caller, because one is
		// a bad id and the other is a lost race.
		if _, err := s.Get(ctx, tenantID, id); err != nil {
			return nil, err
		}
		return nil, emailfilter.ErrAlreadyReviewed
	}
	return s.Get(ctx, tenantID, id)
}

const expireFiltered = `
UPDATE filtered_messages
   SET status = 'EXPIRED'
 WHERE id IN (
     SELECT id FROM filtered_messages
      WHERE status = 'PENDING' AND expires_at IS NOT NULL AND expires_at <= $1
      ORDER BY expires_at
      LIMIT $2
 )`

func (s *quarantineStore) Expire(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = maxPageSize
	}
	conn, err := s.getDB()
	if err != nil {
		return 0, err
	}
	result, err := conn.ExecContext(ctx, expireFiltered, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

func scanFiltered(row scanner) (*emailfilter.FilteredMessage, error) {
	var (
		m                                  emailfilter.FilteredMessage
		direction, action, status          string
		ruleID, messageID, providerID      sql.NullString
		payloadRef, reviewedBy, reviewNote sql.NullString
		recipients, names, matched         []byte
		reviewedAt, expiresAt              sql.NullTime
	)
	if err := row.Scan(&m.ID, &m.TenantID, &direction, &ruleID, &m.RuleName, &action, &status,
		&messageID, &providerID, &m.From, &recipients, &m.Subject, &m.SizeBytes,
		&m.AttachmentCount, &names, &matched, &payloadRef, &reviewedBy, &reviewedAt,
		&reviewNote, &m.CreatedAt, &expiresAt); err != nil {
		return nil, err
	}

	m.Direction = emailfilter.Direction(direction)
	m.Action = emailfilter.Action(action)
	m.Status = emailfilter.Status(status)
	m.RuleID, m.MessageID, m.ProviderID = ruleID.String, messageID.String, providerID.String
	m.PayloadRef, m.ReviewedBy, m.ReviewNote = payloadRef.String, reviewedBy.String, reviewNote.String
	if reviewedAt.Valid {
		m.ReviewedAt = &reviewedAt.Time
	}
	if expiresAt.Valid {
		m.ExpiresAt = &expiresAt.Time
	}

	if err := json.Unmarshal(recipients, &m.Recipients); err != nil {
		return nil, fmt.Errorf("filtered message %s has unreadable recipients: %w", m.ID, err)
	}
	if len(names) > 0 {
		if err := json.Unmarshal(names, &m.AttachmentNames); err != nil {
			return nil, fmt.Errorf("filtered message %s has unreadable attachment names: %w", m.ID, err)
		}
	}
	if err := json.Unmarshal(matched, &m.Matched); err != nil {
		return nil, fmt.Errorf("filtered message %s has unreadable match record: %w", m.ID, err)
	}
	return &m, nil
}

// nullable keeps an empty optional out of the column, so a foreign key or a
// uniqueness check sees NULL rather than the empty string.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func joinAnd(clauses []string) string {
	out := clauses[0]
	for _, clause := range clauses[1:] {
		out += " AND " + clause
	}
	return out
}
