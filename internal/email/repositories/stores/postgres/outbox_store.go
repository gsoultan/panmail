package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/outbox_stats.sql
	outboxStatsQuery string
	//go:embed sql/prune_terminal_outbox.sql
	pruneTerminalOutboxQuery string

	//go:embed sql/create_outbox.sql
	createOutboxQuery string
	//go:embed sql/get_outbox_by_id.sql
	getOutboxByIDQuery string
	//go:embed sql/list_pending_outbox.sql
	listPendingOutboxQuery string
	//go:embed sql/claim_pending_outbox.sql
	claimPendingOutboxQuery string
	//go:embed sql/list_claimed_outbox.sql
	listClaimedOutboxQuery string
	//go:embed sql/release_outbox_claim.sql
	releaseOutboxClaimQuery string
	//go:embed sql/update_outbox.sql
	updateOutboxQuery string
	//go:embed sql/delete_outbox.sql
	deleteOutboxQuery string
	//go:embed sql/count_pending_outbox.sql
	countPendingOutboxQuery string
)

type outboxStore struct {
	conn db.Connection
}

func NewOutboxStore(conn db.Connection) stores.OutboxRepository {
	return &outboxStore{conn: conn}
}

func (s *outboxStore) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *outboxStore) Create(ctx context.Context, email *entities.OutboxEmail) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createOutboxQuery, email.ID, email.TenantID, string(email.Request), email.Status, email.RetryCount, email.NextRetryAt, email.LastError, email.CreatedAt, email.UpdatedAt)
	return err
}

func (s *outboxStore) GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	e := &entities.OutboxEmail{}
	var lastError sql.NullString
	err = db.QueryRowContext(ctx, getOutboxByIDQuery, id).Scan(&e.ID, &e.TenantID, &e.Request, &e.Status, &e.RetryCount, &e.NextRetryAt, &lastError, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	e.LastError = lastError.String
	return e, nil
}

// ClaimPending marks a batch of due messages as this worker's, then reads back
// exactly the rows it won.
//
// The claim is a single UPDATE, so concurrent workers cannot both take a row:
// whichever UPDATE commits first moves those rows to SENDING, and the other's
// predicate no longer matches them. Rows whose lease has lapsed are eligible
// again, which is how work is recovered from a worker that died mid-send.
func (s *outboxStore) ClaimPending(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.OutboxEmail, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	token := uuid.New().String()

	res, err := dbConn.ExecContext(ctx, claimPendingOutboxQuery,
		token, now.Add(leaseFor), now, now, limit)
	if err != nil {
		return nil, err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return nil, nil
	}

	rows, err := dbConn.QueryContext(ctx, listClaimedOutboxQuery, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var emails []*entities.OutboxEmail
	for rows.Next() {
		e := &entities.OutboxEmail{}
		var lastError sql.NullString
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Request, &e.Status, &e.RetryCount, &e.NextRetryAt, &lastError, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.LastError = lastError.String
		emails = append(emails, e)
	}

	return emails, rows.Err()
}

func (s *outboxStore) Update(ctx context.Context, email *entities.OutboxEmail) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, updateOutboxQuery, email.Status, email.RetryCount, email.NextRetryAt, email.LastError, email.UpdatedAt, email.ID)
	return err
}

func (s *outboxStore) Delete(ctx context.Context, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteOutboxQuery, id)
	return err
}

func (s *outboxStore) CountPending(ctx context.Context, tenantID string) (int64, error) {
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}
	var count int64
	err = db.QueryRowContext(ctx, countPendingOutboxQuery, tenantID).Scan(&count)
	return count, err
}

func (s *outboxStore) PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error) {
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, pruneTerminalOutboxQuery, olderThan)
	if err != nil {
		return 0, err
	}
	// Not every driver reports affected rows, and the count is only for the
	// log line, so a driver that declines to say is not an error.
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return removed, nil
}

func (s *outboxStore) Stats(ctx context.Context) (int64, time.Time, error) {
	db, err := s.getDB()
	if err != nil {
		return 0, time.Time{}, err
	}
	var pending int64
	var oldest sql.NullString
	if err := db.QueryRowContext(ctx, outboxStatsQuery).Scan(&pending, &oldest); err != nil {
		return 0, time.Time{}, err
	}
	return pending, parseStoredTime(oldest), nil
}
