package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

// Store reads and writes delivery events and stored messages in the shared
// database, and delegates the rest to a local store.
//
// The split is not arbitrary. Events and messages are what the dashboard reads
// and what every instance must agree about — that is the correctness problem
// docs/design/0002-shared-event-store.md exists for. Archives are files on a
// disk and resource metrics describe *this* process; neither means anything
// pooled across instances, so both stay where they are.
//
// Writes go through the same batching Writer used for the shadow phase, so the
// send path's cost for an event is still one channel send.
type Store struct {
	conn   db.Connection
	writer *Writer

	// local is the Pebble store. It keeps archives and resource metrics, it is
	// what Close closes, and unless stopPebble is set it still receives every
	// event and message.
	local stores.EventRepository

	// stopPebble ends the local event and message writes.
	//
	// Off by default, and that is the whole safety story for switching reads:
	// while Pebble is still written, reverting to it is a restart, because it
	// has everything. Setting this is the step that cannot be undone that way —
	// events written while it is on exist only in the database, so a revert
	// leaves a hole exactly as wide as the time it was set.
	stopPebble bool
}

// Asserted at compile time, because the interface has fifteen methods split
// across two files and a delegation list. A method added to EventRepository
// that nobody implements here should fail the build rather than surface as a
// nil panic the first time the dashboard calls it.
var _ stores.EventRepository = (*Store)(nil)

// NewStore composes the shared store over a local one. Reads come from the
// database; writes go to both, so a revert is a restart.
func NewStore(conn db.Connection, local stores.EventRepository, writer *Writer) *Store {
	return &Store{conn: conn, local: local, writer: writer}
}

// WithoutLocalWrites stops the local event and message writes.
//
// The last step of docs/design/0002-shared-event-store.md, and the only one a
// restart does not undo. Separate from NewStore so that taking it is a
// deliberate act rather than a consequence of switching reads — which is the
// distinction the first version of this store lost by writing only to the
// database as soon as reads moved.
func (s *Store) WithoutLocalWrites() *Store {
	s.stopPebble = true
	return s
}

func (s *Store) db() (*sql.DB, error) {
	if s.conn == nil || !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *Store) isPostgres() bool {
	conn, err := s.db()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", conn.Driver())), "stdlib")
}

// Write queues an event on the batching writer.
//
// It returns nil rather than the writer's outcome because there is no outcome
// to return: the write is asynchronous by design, and a caller on the send
// path has nothing useful to do with a failure it would learn about later.
// panmail_event_shadow_failed_total is where a failure shows up.
func (s *Store) Write(ctx context.Context, e *entities.EmailEvent) error {
	s.writer.Write(e)
	if s.stopPebble {
		return nil
	}
	// Still written locally, so that reverting to Pebble reads is a restart
	// rather than an acceptance of a gap. Its failure is not returned: the
	// database has the event, and nothing reads Pebble in this mode.
	if err := s.local.Write(ctx, e); err != nil {
		slog.Warn("could not mirror a delivery event to the local store",
			"error", err, "id", e.ID)
	}
	return nil
}

const messageColumns = `id, tenant_id, provider_id, from_address, to_addresses,
	cc_addresses, bcc_addresses, subject, body_html, body_text, attachments, created_at`

// WriteMessage stores the message body.
//
// Synchronous, unlike events. There is one per message rather than three, it
// happens once at admission rather than on every state change, and the Content
// tab is unreadable without it — so the ordering guarantee is worth the write.
func (s *Store) WriteMessage(ctx context.Context, m *entities.EmailMessage) error {
	conn, err := s.db()
	if err != nil {
		return err
	}
	if m == nil {
		return errors.New("message is required")
	}

	_, err = conn.ExecContext(ctx,
		`INSERT INTO email_messages (`+messageColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 ON CONFLICT (id) DO UPDATE SET
		   provider_id = COALESCE(EXCLUDED.provider_id, email_messages.provider_id)`,
		m.ID, m.TenantID, nullableUUID(m.ProviderID), m.From,
		encodeList(m.To), encodeList(m.Cc), encodeList(m.Bcc),
		nullableText(m.Subject), nullableText(m.BodyHTML), nullableText(m.BodyText),
		encodeAttachments(m.Attachments), m.CreatedAt.UTC())
	if err != nil {
		return err
	}

	if s.stopPebble {
		return nil
	}
	if err := s.local.WriteMessage(ctx, m); err != nil {
		slog.Warn("could not mirror a stored message to the local store",
			"error", err, "id", m.ID)
	}
	return nil
}

func (s *Store) GetMessage(ctx context.Context, tenantID, messageID string) (*entities.EmailMessage, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT `+messageColumns+` FROM email_messages WHERE tenant_id = $1 AND id = $2`,
		tenantID, messageID)

	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// GetLatestMessageForRecipient is the fallback used when an event arrives with
// no message id — a provider webhook that names only the address.
//
// It joins through the events table rather than searching the recipient lists,
// because the lists are stored whole and are not queryable, and because an
// event is the only record that ties an address to a message.
func (s *Store) GetLatestMessageForRecipient(ctx context.Context, tenantID, recipient string) (*entities.EmailMessage, error) {
	conn, err := s.db()
	if err != nil {
		return nil, err
	}
	row := conn.QueryRowContext(ctx,
		`SELECT `+prefixed("m", messageColumns)+`
		 FROM email_messages m
		 WHERE m.tenant_id = $1 AND m.id = (
		   SELECT e.message_id FROM email_events e
		   WHERE e.tenant_id = $1 AND e.recipient = $2
		   ORDER BY e.timestamp DESC LIMIT 1)`,
		tenantID, recipient)

	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// TruncateMessagesBefore deletes stored bodies older than the cutoff.
func (s *Store) TruncateMessagesBefore(ctx context.Context, before time.Time) (int64, error) {
	conn, err := s.db()
	if err != nil {
		return 0, err
	}
	res, err := conn.ExecContext(ctx,
		`DELETE FROM email_messages WHERE created_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- delegated to the local store ---
//
// Archives are files on this instance's disk and resource metrics describe
// this process. Neither is meaningful pooled across instances, so both stay.

func (s *Store) ListArchives(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]entities.ArchiveInfo, string, error) {
	return s.local.ListArchives(ctx, tenantID, pageSize, pageToken)
}

func (s *Store) GetArchive(ctx context.Context, tenantID, id string) ([]byte, string, error) {
	return s.local.GetArchive(ctx, tenantID, id)
}

func (s *Store) PruneArchivesBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.local.PruneArchivesBefore(ctx, before)
}

func (s *Store) WriteResourceMetric(ctx context.Context, cpu float64, mem uint64, load15 float64) error {
	return s.local.WriteResourceMetric(ctx, cpu, mem, load15)
}

func (s *Store) GetResourceHistory(ctx context.Context, since time.Time) ([]entities.ResourcePoint, error) {
	return s.local.GetResourceHistory(ctx, since)
}

// Close stops the writer and then closes the local store.
//
// In that order: the writer's final flush needs the database, and closing the
// local store first would be closing the thing archives are served from while
// a flush is still in flight.
func (s *Store) Close() error {
	if s.writer != nil {
		s.writer.Stop()
	}
	return s.local.Close()
}

// --- encoding ---

func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func encodeList(v []string) any {
	if len(v) == 0 {
		return nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(encoded)
}

// encodeAttachments stores the attachments as JSON.
//
// encoding/json rather than protojson, deliberately and against the instinct
// that generated protobuf types want protojson. This column is not a public
// contract — nothing outside this store reads it — and the Pebble store it
// replaces marshalled the whole entity with encoding/json. Matching that means
// a message written before the move and one written after decode identically,
// which is what step three depends on. Content is a []byte field and
// encoding/json base64s it, so it round-trips intact.
func encodeAttachments(a []*panmailv1.Attachment) any {
	if len(a) == 0 {
		return nil
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	return string(encoded)
}

func scanMessage(row rowScanner) (*entities.EmailMessage, error) {
	var (
		m           entities.EmailMessage
		providerID  sql.NullString
		to, cc, bcc sql.NullString
		subject     sql.NullString
		bodyHTML    sql.NullString
		bodyText    sql.NullString
		attachments sql.NullString
	)
	if err := row.Scan(&m.ID, &m.TenantID, &providerID, &m.From, &to, &cc, &bcc,
		&subject, &bodyHTML, &bodyText, &attachments, &m.CreatedAt); err != nil {
		return nil, err
	}

	m.ProviderID = providerID.String
	m.Subject = subject.String
	m.BodyHTML = bodyHTML.String
	m.BodyText = bodyText.String

	decodeList(to, &m.To)
	decodeList(cc, &m.Cc)
	decodeList(bcc, &m.Bcc)

	if attachments.Valid && attachments.String != "" {
		_ = json.Unmarshal([]byte(attachments.String), &m.Attachments)
	}
	return &m, nil
}

func decodeList(v sql.NullString, into *[]string) {
	if v.Valid && v.String != "" {
		_ = json.Unmarshal([]byte(v.String), into)
	}
}
