package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

const eventColumns = `id, tenant_id, provider_id, provider_name, message_id,
	type, recipient, subject, timestamp, metadata, error_message`

// List returns a tenant's events, newest first, with keyset pagination.
//
// Keyset rather than OFFSET, matching the quarantine store: events are written
// while they are being read, and an offset page skips or repeats rows as new
// ones arrive. The token is the timestamp and id of the last row returned,
// because timestamp alone is not unique — three events for one message can
// share a microsecond, and a token that dropped the id would lose whichever of
// them sorted after it.
func (s *Store) List(ctx context.Context, tenantID string, filter stores.ListFilter) ([]*entities.EmailEvent, string, error) {
	conn, err := s.db()
	if err != nil {
		return nil, "", err
	}

	size := filter.PageSize
	if size <= 0 || size > 500 {
		size = 50
	}

	where := []string{"tenant_id = $1"}
	args := []any{tenantID}

	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if filter.MessageID != "" {
		add("message_id = $%d", filter.MessageID)
	}
	if filter.Recipient != "" {
		if filter.RecipientExact {
			add("recipient = $%d", filter.Recipient)
		} else {
			add("recipient "+s.likeOperator()+" $%d", "%"+filter.Recipient+"%")
		}
	}
	if filter.EventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED {
		add("type = $%d", filter.EventType.String())
	}
	if filter.Subject != "" {
		add("subject "+s.likeOperator()+" $%d", "%"+filter.Subject+"%")
	}
	if !filter.StartTime.IsZero() {
		add("timestamp >= $%d", filter.StartTime.UTC())
	}
	if !filter.EndTime.IsZero() {
		add("timestamp <= $%d", filter.EndTime.UTC())
	}

	if filter.PageToken != "" {
		ts, id, err := decodePageToken(filter.PageToken)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf(
			"(timestamp, id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, size+1)
	statement := `SELECT ` + eventColumns + ` FROM email_events WHERE ` +
		strings.Join(where, " AND ") +
		` ORDER BY timestamp DESC, id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	events := make([]*entities.EmailEvent, 0, size)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, "", err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	// One more than the page was asked for is how we know there is another
	// page without counting the whole table.
	var next string
	if len(events) > size {
		last := events[size-1]
		next = encodePageToken(last.Timestamp, last.ID)
		events = events[:size]
	}

	// LatestOnly keeps one event per message: the newest. The rows already
	// arrive newest first, so the first sighting of a message id wins.
	if filter.LatestOnly {
		seen := make(map[string]struct{}, len(events))
		latest := events[:0]
		for _, e := range events {
			if _, dup := seen[e.MessageID]; dup {
				continue
			}
			seen[e.MessageID] = struct{}{}
			latest = append(latest, e)
		}
		events = latest
	}

	return events, next, nil
}

func (s *Store) GetByID(ctx context.Context, tenantID, id string) (*entities.EmailEvent, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM email_events WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)

	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		// Absent is not an error: the caller asked whether it exists.
		return nil, nil
	}
	return e, err
}

// ListByMessageID returns one message's timeline, oldest first, because that is
// the order a timeline is read in.
func (s *Store) ListByMessageID(ctx context.Context, tenantID, messageID string) ([]*entities.EmailEvent, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM email_events
		 WHERE tenant_id = $1 AND message_id = $2 ORDER BY timestamp ASC, id ASC`,
		tenantID, messageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []*entities.EmailEvent
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetMetrics counts events by type.
//
// A zero start and end means all time, which is what the dashboard's headline
// figures ask for. The counts are of events, not of messages — a message that
// was sent and then delivered contributes to both, which is what the Pebble
// counters did too.
func (s *Store) GetMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time) (map[string]int64, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}

	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	if !startTime.IsZero() {
		args = append(args, startTime.UTC())
		where = append(where, fmt.Sprintf("timestamp >= $%d", len(args)))
	}
	if !endTime.IsZero() {
		args = append(args, endTime.UTC())
		where = append(where, fmt.Sprintf("timestamp <= $%d", len(args)))
	}

	rows, err := conn.QueryContext(ctx,
		`SELECT type, count(*) FROM email_events WHERE `+strings.Join(where, " AND ")+
			` GROUP BY type`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int64)
	for rows.Next() {
		var name string
		var count int64
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		// The keys the dashboard uses are the enum name without its prefix,
		// which is what the Pebble store wrote.
		out[strings.TrimPrefix(name, "EMAIL_EVENT_TYPE_")] = count
	}
	return out, rows.Err()
}

// GetTimeSeriesMetrics buckets counts by day or hour.
//
// The bucket is formatted in SQL rather than in Go so the grouping happens in
// the database: pulling every row back to count them in a loop is the shape
// this whole change exists to avoid.
func (s *Store) GetTimeSeriesMetrics(ctx context.Context, tenantID string, startTime, endTime time.Time, granularity string) (map[string]map[string]int64, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}

	bucket := s.bucketExpr(granularity)

	where := []string{"tenant_id = $1"}
	args := []any{tenantID}
	if !startTime.IsZero() {
		args = append(args, startTime.UTC())
		where = append(where, fmt.Sprintf("timestamp >= $%d", len(args)))
	}
	if !endTime.IsZero() {
		args = append(args, endTime.UTC())
		where = append(where, fmt.Sprintf("timestamp <= $%d", len(args)))
	}

	rows, err := conn.QueryContext(ctx,
		`SELECT `+bucket+` AS bucket, type, count(*)
		 FROM email_events WHERE `+strings.Join(where, " AND ")+
			` GROUP BY bucket, type ORDER BY bucket`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]map[string]int64)
	for rows.Next() {
		var bucket, name string
		var count int64
		if err := rows.Scan(&bucket, &name, &count); err != nil {
			return nil, err
		}
		if out[bucket] == nil {
			out[bucket] = make(map[string]int64)
		}
		out[bucket][strings.TrimPrefix(name, "EMAIL_EVENT_TYPE_")] = count
	}
	return out, rows.Err()
}

// TruncateBefore deletes events older than the cutoff, for internal/retention.
//
// This is the change the design doc called out as an improvement: a Pebble
// key-range prune per instance becomes one DELETE that every instance shares.
func (s *Store) TruncateBefore(ctx context.Context, before time.Time) (int64, error) {
	conn, err := s.db()
	if err != nil {
		return 0, err
	}
	res, err := conn.ExecContext(ctx,
		`DELETE FROM email_events WHERE timestamp < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanEvent(row rowScanner) (*entities.EmailEvent, error) {
	var (
		e            entities.EmailEvent
		providerID   sql.NullString
		providerName sql.NullString
		subject      sql.NullString
		metadata     sql.NullString
		errorMessage sql.NullString
		typeName     string
	)
	if err := row.Scan(&e.ID, &e.TenantID, &providerID, &providerName, &e.MessageID,
		&typeName, &e.Recipient, &subject, &e.Timestamp, &metadata, &errorMessage); err != nil {
		return nil, err
	}

	e.ProviderID = providerID.String
	e.ProviderName = providerName.String
	e.Subject = subject.String
	e.ErrorMessage = errorMessage.String

	// By name, so a type this build does not know reads as UNSPECIFIED rather
	// than silently matching another type's number.
	e.Type = panmailv1.EmailEventType(panmailv1.EmailEventType_value[typeName])

	if metadata.Valid && metadata.String != "" {
		_ = json.Unmarshal([]byte(metadata.String), &e.Metadata)
	}
	return &e, nil
}

// The token carries both halves of the sort key. Two events can share a
// timestamp, and a token that dropped the id would lose whichever sorted after
// it on the page boundary.
func encodePageToken(ts time.Time, id string) string {
	return ts.UTC().Format(time.RFC3339Nano) + "|" + id
}

func decodePageToken(token string) (time.Time, string, error) {
	at, id, found := strings.Cut(token, "|")
	if !found {
		return time.Time{}, "", fmt.Errorf("malformed page token")
	}
	ts, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("malformed page token: %w", err)
	}
	return ts, id, nil
}

// likeOperator is the case-insensitive match each engine spells differently.
//
// ILIKE is PostgreSQL's and is a syntax error on SQLite; SQLite's LIKE is
// already case-insensitive for ASCII, which is what an address or a subject
// search wants. This is the same divergence that made provider name search a
// reason to run --db postgres, and the same shape of fix as the outbox's
// dialect-specific claim.
func (s *Store) likeOperator() string {
	if s.isPostgres() {
		return "ILIKE"
	}
	return "LIKE"
}

// bucketExpr formats a timestamp into a day or hour bucket, in the database.
//
// Grouping in SQL rather than pulling every row back to count it in a loop is
// the point of moving this store; doing it in Go would keep the cost this
// change exists to remove.
func (s *Store) bucketExpr(granularity string) string {
	if s.isPostgres() {
		if granularity == "hour" {
			return `to_char(timestamp, 'YYYY-MM-DD HH24')`
		}
		return `to_char(timestamp, 'YYYY-MM-DD')`
	}
	// substr, not strftime, and this is not a stylistic choice.
	//
	// SQLite stores what the driver hands it, and the driver hands it Go's
	// String() form: "2026-09-06 08:08:21.841566 +0000 UTC". strftime cannot
	// parse the trailing offset and zone, so it returns NULL for every row —
	// silently, as an empty chart rather than an error. The same shape as the
	// outbox trap that parseStoredTime exists for.
	//
	// The prefix is already ISO-ordered, so a substring is the bucket: ten
	// characters is the date, thirteen reaches the hour, and both sort
	// correctly as text.
	if granularity == "hour" {
		return `substr(CAST(timestamp AS TEXT), 1, 13)`
	}
	return `substr(CAST(timestamp AS TEXT), 1, 10)`
}
