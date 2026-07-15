package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_outbox.sql
	createOutboxQuery string
	//go:embed sql/get_outbox_by_id.sql
	getOutboxByIDQuery string
	//go:embed sql/list_pending_outbox.sql
	listPendingOutboxQuery string
	//go:embed sql/update_outbox.sql
	updateOutboxQuery string
	//go:embed sql/delete_outbox.sql
	deleteOutboxQuery string
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
	_, err = db.ExecContext(ctx, createOutboxQuery, email.ID, email.TenantID, email.Request, email.Status, email.RetryCount, email.NextRetryAt, email.LastError, email.CreatedAt, email.UpdatedAt)
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

func (s *outboxStore) ListPending(ctx context.Context, limit int) ([]*entities.OutboxEmail, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, listPendingOutboxQuery, time.Now(), limit)
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
	return emails, nil
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
