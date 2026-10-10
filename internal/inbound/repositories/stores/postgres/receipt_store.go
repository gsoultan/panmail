// Package postgres keeps the inbound endpoint's receipts in the shared SQL
// database. The mail itself stays in Pebble; see the 0020 migration for why the
// receipts cannot.
package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/insert_receipt.sql
	insertReceiptQuery string
	//go:embed sql/take_over_receipt.sql
	takeOverReceiptQuery string
	//go:embed sql/get_receipt_state.sql
	getReceiptStateQuery string
	//go:embed sql/complete_receipt.sql
	completeReceiptQuery string
	//go:embed sql/release_receipt.sql
	releaseReceiptQuery string
	//go:embed sql/prune_receipts.sql
	pruneReceiptsQuery string
)

// stateDone is a receipt whose message is committed. The other state,
// PROCESSING, is only ever written and compared in SQL.
const stateDone = "DONE"

type receiptStore struct {
	conn db.Connection
	now  func() time.Time
}

func NewReceiptStore(conn db.Connection) stores.ReceiptRepository {
	return &receiptStore{conn: conn, now: time.Now}
}

func (s *receiptStore) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *receiptStore) Claim(ctx context.Context, tenantID, providerID, key string, lease time.Duration) (stores.ClaimResult, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return 0, "", err
	}

	// Twice at most: the row can vanish between the attempts below, when the
	// post holding it gives up and releases it. The second pass then inserts
	// afresh. A third disappearance in a row is not worth chasing; the caller
	// is told to come back, which costs one provider retry.
	for range 2 {
		now := s.now().Unix()
		token := uuid.NewString()

		res, err := dbConn.ExecContext(ctx, insertReceiptQuery, tenantID, providerID, key, token, now)
		if err != nil {
			return 0, "", err
		}
		if n, err := res.RowsAffected(); err == nil && n == 1 {
			return stores.ClaimAcquired, token, nil
		}

		res, err = dbConn.ExecContext(ctx, takeOverReceiptQuery,
			tenantID, providerID, key, token, now, now-int64(lease/time.Second))
		if err != nil {
			return 0, "", err
		}
		if n, err := res.RowsAffected(); err == nil && n == 1 {
			return stores.ClaimAcquired, token, nil
		}

		var state string
		err = dbConn.QueryRowContext(ctx, getReceiptStateQuery, tenantID, providerID, key).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return 0, "", err
		case state == stateDone:
			return stores.ClaimDuplicate, "", nil
		default:
			return stores.ClaimInFlight, "", nil
		}
	}
	return stores.ClaimInFlight, "", nil
}

func (s *receiptStore) Complete(ctx context.Context, tenantID, providerID, key, token string) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = dbConn.ExecContext(ctx, completeReceiptQuery, tenantID, providerID, key, token)
	return err
}

func (s *receiptStore) Release(ctx context.Context, tenantID, providerID, key, token string) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = dbConn.ExecContext(ctx, releaseReceiptQuery, tenantID, providerID, key, token)
	return err
}

func (s *receiptStore) PruneBefore(ctx context.Context, before time.Time) (int64, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return 0, err
	}
	res, err := dbConn.ExecContext(ctx, pruneReceiptsQuery, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
