package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

//go:embed sql/create_webhook.sql
var createWebhookQuery string

//go:embed sql/list_webhooks.sql
var listWebhooksQuery string

//go:embed sql/get_webhook_by_id.sql
var getWebhookByIDQuery string

//go:embed sql/update_webhook.sql
var updateWebhookQuery string

//go:embed sql/delete_webhook.sql
var deleteWebhookQuery string

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) stores.WebhookRepository {
	return &store{conn: conn}
}

func (s *store) Create(ctx context.Context, webhook *entities.Webhook) error {
	dbConn := s.conn.GetDB()
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return err
	}

	_, err = dbConn.ExecContext(ctx, createWebhookQuery,
		webhook.ID,
		webhook.TenantID,
		webhook.Name,
		webhook.URL,
		string(eventsJSON),
		webhook.Active,
		webhook.CreatedAt,
		webhook.UpdatedAt,
	)
	return err
}

func (s *store) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Webhook, string, error) {
	dbConn := s.conn.GetDB()
	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listWebhooksQuery), ";")
	query += " LIMIT $2 OFFSET $3"

	rows, err := dbConn.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var webhooks []*entities.Webhook
	for rows.Next() {
		var w entities.Webhook
		var eventsJSON []byte
		err := rows.Scan(
			&w.ID,
			&w.TenantID,
			&w.Name,
			&w.URL,
			&eventsJSON,
			&w.Active,
			&w.CreatedAt,
			&w.UpdatedAt,
		)
		if err != nil {
			return nil, "", err
		}
		if err := json.Unmarshal(eventsJSON, &w.Events); err != nil {
			return nil, "", err
		}
		webhooks = append(webhooks, &w)
	}

	nextPageToken := ""
	if len(webhooks) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return webhooks, nextPageToken, nil
}

func (s *store) GetByID(ctx context.Context, tenantID, id string) (*entities.Webhook, error) {
	dbConn := s.conn.GetDB()
	var w entities.Webhook
	var eventsJSON []byte
	err := dbConn.QueryRowContext(ctx, getWebhookByIDQuery, tenantID, id).Scan(
		&w.ID,
		&w.TenantID,
		&w.Name,
		&w.URL,
		&eventsJSON,
		&w.Active,
		&w.CreatedAt,
		&w.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(eventsJSON, &w.Events); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *store) Update(ctx context.Context, webhook *entities.Webhook) error {
	dbConn := s.conn.GetDB()
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return err
	}

	_, err = dbConn.ExecContext(ctx, updateWebhookQuery,
		webhook.Name,
		webhook.URL,
		string(eventsJSON),
		webhook.Active,
		webhook.UpdatedAt,
		webhook.TenantID,
		webhook.ID,
	)
	return err
}

func (s *store) Delete(ctx context.Context, tenantID, id string) error {
	dbConn := s.conn.GetDB()
	_, err := dbConn.ExecContext(ctx, deleteWebhookQuery, tenantID, id)
	return err
}

func (s *store) ListActiveByEvent(ctx context.Context, tenantID string, event int32) ([]*entities.Webhook, error) {
	dbConn := s.conn.GetDB()
	rows, err := dbConn.QueryContext(ctx, listWebhooksQuery, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var active []*entities.Webhook
	for rows.Next() {
		var w entities.Webhook
		var eventsJSON []byte
		err := rows.Scan(
			&w.ID,
			&w.TenantID,
			&w.Name,
			&w.URL,
			&eventsJSON,
			&w.Active,
			&w.CreatedAt,
			&w.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if !w.Active {
			continue
		}
		if err := json.Unmarshal(eventsJSON, &w.Events); err != nil {
			return nil, err
		}
		for _, e := range w.Events {
			if e == event {
				active = append(active, &w)
				break
			}
		}
	}
	return active, nil
}
