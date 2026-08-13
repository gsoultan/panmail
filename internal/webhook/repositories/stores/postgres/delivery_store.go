package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_delivery.sql
	createDeliveryQuery string
	//go:embed sql/claim_due_deliveries.sql
	claimDueDeliveriesQuery string
	//go:embed sql/list_claimed_deliveries.sql
	listClaimedDeliveriesQuery string
	//go:embed sql/update_delivery.sql
	updateDeliveryQuery string
	//go:embed sql/delete_delivery.sql
	deleteDeliveryQuery string
	//go:embed sql/prune_terminal_deliveries.sql
	pruneTerminalDeliveriesQuery string
)

type deliveryStore struct {
	conn db.Connection
}

func NewDeliveryStore(conn db.Connection) stores.DeliveryRepository {
	return &deliveryStore{conn: conn}
}

func (s *deliveryStore) getDB() (*sql.DB, error) {
	if s.conn == nil || !s.conn.IsConnected() {
		return nil, errors.New("database is not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *deliveryStore) Create(ctx context.Context, d *entities.WebhookDelivery) error {
	database, err := s.getDB()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	// Normalised here rather than trusted from the caller.
	//
	// SQLite stores whatever offset the driver renders and compares timestamps
	// as strings, so a row written as +07:00 is never found by a query asking
	// for "<= now" in UTC — the same instant, ordered wrongly. The claim
	// matched nothing at all until this was consistent, which reads as the
	// worker being asleep rather than as a timezone problem.
	d.NextAttemptAt = d.NextAttemptAt.UTC()
	d.CreatedAt = d.CreatedAt.UTC()
	if d.Status == "" {
		d.Status = entities.DeliveryStatusPending
	}
	if d.NextAttemptAt.IsZero() {
		d.NextAttemptAt = now
	}

	_, err = database.ExecContext(ctx, createDeliveryQuery,
		d.ID, d.TenantID, d.WebhookID, d.Event, d.Payload, string(d.Status),
		d.AttemptCount, d.NextAttemptAt, d.LastError, d.CreatedAt, d.UpdatedAt)
	return err
}

func (s *deliveryStore) ClaimDue(ctx context.Context, limit int, leaseFor time.Duration) ([]*entities.WebhookDelivery, error) {
	database, err := s.getDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	token := uuid.New().String()

	// Claim, then read back by token. Doing it in two statements rather than
	// one RETURNING keeps the query the same across engines.
	if _, err := database.ExecContext(ctx, claimDueDeliveriesQuery,
		token, now.Add(leaseFor), now, now, limit); err != nil {
		return nil, err
	}

	rows, err := database.QueryContext(ctx, listClaimedDeliveriesQuery, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var claimed []*entities.WebhookDelivery
	for rows.Next() {
		d := &entities.WebhookDelivery{}
		var status string
		if err := rows.Scan(&d.ID, &d.TenantID, &d.WebhookID, &d.Event, &d.Payload, &status,
			&d.AttemptCount, &d.NextAttemptAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Status = entities.DeliveryStatus(status)
		claimed = append(claimed, d)
	}
	return claimed, rows.Err()
}

func (s *deliveryStore) Update(ctx context.Context, d *entities.WebhookDelivery) error {
	database, err := s.getDB()
	if err != nil {
		return err
	}
	d.UpdatedAt = time.Now().UTC()
	// Same reason as Create: every timestamp this table holds is UTC, or the
	// comparisons that drive the queue silently match nothing.
	d.NextAttemptAt = d.NextAttemptAt.UTC()
	_, err = database.ExecContext(ctx, updateDeliveryQuery,
		d.ID, string(d.Status), d.AttemptCount, d.NextAttemptAt, d.LastError, d.UpdatedAt)
	return err
}

func (s *deliveryStore) Delete(ctx context.Context, id string) error {
	database, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, deleteDeliveryQuery, id)
	return err
}

func (s *deliveryStore) PruneTerminal(ctx context.Context, olderThan time.Time) (int64, error) {
	database, err := s.getDB()
	if err != nil {
		return 0, err
	}
	res, err := database.ExecContext(ctx, pruneTerminalDeliveriesQuery, olderThan)
	if err != nil {
		return 0, err
	}
	// Not every driver reports affected rows, and the count is only for a log
	// line, so a driver that declines to say is not an error.
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return removed, nil
}
