package postgres

import (
	"fmt"

	"context"
	_ "embed"
	"encoding/json"
	"github.com/gsoultan/panmail/pkg/secrets"
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
	// The signing secret is a credential: anyone holding it can forge a
	// notification the tenant will believe. Encrypted at rest like the
	// provider passwords, and readable only by the delivery worker.
	keyring *secrets.Keyring
}

func NewStore(conn db.Connection, keyring *secrets.Keyring) stores.WebhookRepository {
	return &store{conn: conn, keyring: keyring}
}

func (s *store) Create(ctx context.Context, webhook *entities.Webhook) error {
	dbConn := s.conn.GetDB()
	eventsJSON, err := json.Marshal(webhook.Events)
	if err != nil {
		return err
	}

	sealed, err := s.keyring.Encrypt(webhook.Secret)
	if err != nil {
		return fmt.Errorf("failed to encrypt the webhook signing secret: %w", err)
	}

	_, err = dbConn.ExecContext(ctx, createWebhookQuery,
		webhook.ID,
		webhook.TenantID,
		webhook.Name,
		webhook.URL,
		string(eventsJSON),
		webhook.Active,
		sealed,
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
			&w.Secret,
			&w.CreatedAt,
			&w.UpdatedAt,
		)
		if err != nil {
			return nil, "", err
		}
		if w.Secret, err = s.keyring.Decrypt(w.Secret); err != nil {
			return nil, "", fmt.Errorf("failed to decrypt the webhook signing secret: %w", err)
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
		&w.Secret,
		&w.CreatedAt,
		&w.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	// GetByID is what the delivery worker calls, so this is the read that has
	// to yield a usable signing secret.
	if w.Secret, err = s.keyring.Decrypt(w.Secret); err != nil {
		return nil, fmt.Errorf("failed to decrypt the webhook signing secret: %w", err)
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
			&w.Secret,
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

// RotateSecrets rewrites every stored signing secret under the keyring's
// primary key.
//
// Without this a rotation is incomplete in a way that only shows up after the
// old key is dropped: the provider credentials move, the webhook secrets do
// not, and every later read fails to decrypt. The documented rotation would
// then silently stop webhook delivery.
//
// Values already on the primary key are skipped, so the pass is idempotent and
// an interrupted run resumes by running it again. Not tenant-scoped: a key
// still holding one tenant's rows is a key that cannot be retired.
func (s *store) RotateSecrets(ctx context.Context) (rotated int, err error) {
	dbConn := s.conn.GetDB()

	type row struct{ id, secret string }

	// Read everything first: holding a cursor open while updating the same
	// table deadlocks on some engines and re-reads rewritten rows on others.
	rows, err := dbConn.QueryContext(ctx, `SELECT id, COALESCE(secret, '') FROM webhooks`)
	if err != nil {
		return 0, err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.secret); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, r := range all {
		if !s.keyring.NeedsRotation(r.secret) {
			continue
		}

		// A failure here means a key is missing, and rewriting the row anyway
		// would replace a recoverable secret with an unreadable one.
		plain, err := s.keyring.Decrypt(r.secret)
		if err != nil {
			return rotated, fmt.Errorf("webhook %s: %w", r.id, err)
		}
		sealed, err := s.keyring.Encrypt(plain)
		if err != nil {
			return rotated, fmt.Errorf("webhook %s: %w", r.id, err)
		}
		if _, err := dbConn.ExecContext(ctx, `UPDATE webhooks SET secret = $2 WHERE id = $1`, r.id, sealed); err != nil {
			return rotated, fmt.Errorf("webhook %s: %w", r.id, err)
		}
		rotated++
	}

	return rotated, nil
}
