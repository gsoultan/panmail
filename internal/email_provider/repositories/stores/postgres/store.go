package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/secrets"
)

var (
	//go:embed sql/rotate_list_secrets.sql
	rotateListSecretsQuery string
	//go:embed sql/rotate_update_secrets.sql
	rotateUpdateSecretsQuery string

	//go:embed sql/create_provider.sql
	createProviderQuery string
	//go:embed sql/get_provider_by_id.sql
	getProviderByIDQuery string
	//go:embed sql/list_providers.sql
	listProvidersQuery string
	//go:embed sql/update_provider.sql
	updateProviderQuery string
	//go:embed sql/delete_provider.sql
	deleteProviderQuery string
)

// selectColumns is the column list shared by every read, kept in one place so
// the dynamic filter query below cannot drift from the embedded ones.
const selectColumns = "id, tenant_id, name, type, config, allowed_domains, webhook_secret, created_at, updated_at"

type store struct {
	conn    db.Connection
	keyring *secrets.Keyring
}

// NewStore builds the provider repository.
//
// The keyring encrypts the provider configuration, which carries SMTP, IMAP
// and POP3 passwords, and the webhook verification secret. Anyone holding a
// database backup or a read replica would otherwise have every credential in
// the clear.
func NewStore(conn db.Connection, keyring *secrets.Keyring) stores.Repository {
	return &store{conn: conn, keyring: keyring}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, p *entities.EmailProvider) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}

	config, secret, err := s.seal(p)
	if err != nil {
		return err
	}
	allowedDomainsJSON, _ := json.Marshal(p.AllowedDomains)

	_, err = db.ExecContext(ctx, createProviderQuery,
		p.ID, p.TenantID, p.Name, p.Type, config, string(allowedDomainsJSON), secret,
		p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *store) GetByID(ctx context.Context, tenantID, id string) (*entities.EmailProvider, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	return s.scanProvider(db.QueryRowContext(ctx, getProviderByIDQuery, tenantID, id).Scan)
}

func (s *store) List(ctx context.Context, tenantID string, name string, providerType string, pageSize int, pageToken string) ([]*entities.EmailProvider, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := "SELECT " + selectColumns + " FROM email_providers WHERE tenant_id = $1"
	args := []any{tenantID}
	argCount := 1

	if name != "" {
		argCount++
		query += fmt.Sprintf(" AND name ILIKE $%d", argCount)
		args = append(args, "%"+name+"%")
	}

	if providerType != "" {
		argCount++
		query += fmt.Sprintf(" AND type = $%d", argCount)
		args = append(args, providerType)
	}

	query += " ORDER BY created_at DESC"

	argCount++
	query += fmt.Sprintf(" LIMIT $%d", argCount)
	args = append(args, pageSize)

	argCount++
	query += fmt.Sprintf(" OFFSET $%d", argCount)
	args = append(args, offset)

	rows, err := dbConn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var providers []*entities.EmailProvider
	for rows.Next() {
		p, err := s.scanProvider(rows.Scan)
		if err != nil {
			return nil, "", err
		}
		providers = append(providers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextPageToken := ""
	if len(providers) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return providers, nextPageToken, nil
}

func (s *store) Update(ctx context.Context, p *entities.EmailProvider) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}

	config, secret, err := s.seal(p)
	if err != nil {
		return err
	}
	allowedDomainsJSON, _ := json.Marshal(p.AllowedDomains)

	_, err = db.ExecContext(ctx, updateProviderQuery,
		p.TenantID, p.ID, p.Name, config, string(allowedDomainsJSON), secret, p.UpdatedAt)
	return err
}

func (s *store) Delete(ctx context.Context, tenantID, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteProviderQuery, tenantID, id)
	return err
}

// seal encrypts the values that must not be stored in the clear.
func (s *store) seal(p *entities.EmailProvider) (config string, secret string, err error) {
	config, err = s.keyring.Encrypt(string(p.Config))
	if err != nil {
		return "", "", fmt.Errorf("failed to encrypt provider configuration: %w", err)
	}

	secret, err = s.keyring.Encrypt(p.WebhookSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to encrypt provider webhook secret: %w", err)
	}

	return config, secret, nil
}

// scanProvider reads one row and decrypts the sealed columns.
func (s *store) scanProvider(scan func(...any) error) (*entities.EmailProvider, error) {
	p := &entities.EmailProvider{}
	var allowedDomainsJSON []byte
	var config string
	var webhookSecret sql.NullString

	if err := scan(&p.ID, &p.TenantID, &p.Name, &p.Type, &config, &allowedDomainsJSON,
		&webhookSecret, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}

	plainConfig, err := s.keyring.Decrypt(config)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt configuration for provider %s: %w", p.ID, err)
	}
	p.Config = []byte(plainConfig)

	if webhookSecret.Valid {
		plainSecret, err := s.keyring.Decrypt(webhookSecret.String)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt webhook secret for provider %s: %w", p.ID, err)
		}
		p.WebhookSecret = plainSecret
	}

	if len(allowedDomainsJSON) > 0 {
		_ = json.Unmarshal(allowedDomainsJSON, &p.AllowedDomains)
	}

	return p, nil
}

// RotateSecrets rewrites every stored secret under the keyring's primary key.
//
// This is the step that makes a key replaceable. Adding a new key and keeping
// the old one lets both be read, but until every value has actually been
// rewritten the old key can never be dropped — and a key that can never be
// dropped is not a rotation, it is an accumulation.
//
// Values already on the primary key are skipped, so the pass is idempotent and
// safe to re-run: an interrupted rotation is resumed simply by running it
// again.
//
// Not tenant-scoped, deliberately. A rotation is an operator action over the
// whole deployment, and a key left holding one tenant's rows is a key that
// still cannot be retired.
func (s *store) RotateSecrets(ctx context.Context) (rotated int, err error) {
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}

	type row struct {
		id     string
		config string
		secret sql.NullString
	}

	// Read everything first. Holding a cursor open while issuing updates on the
	// same table deadlocks on some engines and silently re-reads rewritten rows
	// on others.
	rows, err := db.QueryContext(ctx, rotateListSecretsQuery)
	if err != nil {
		return 0, err
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.config, &r.secret); err != nil {
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
		needsConfig := s.keyring.NeedsRotation(r.config)
		needsSecret := r.secret.Valid && s.keyring.NeedsRotation(r.secret.String)
		if !needsConfig && !needsSecret {
			continue
		}

		// Decrypt with whichever key wrote it, then seal with the primary.
		// A failure here means a key is missing, and continuing would rewrite
		// the row with something unreadable — so stop and say which row.
		plainConfig, err := s.keyring.Decrypt(r.config)
		if err != nil {
			return rotated, fmt.Errorf("provider %s: %w", r.id, err)
		}
		newConfig, err := s.keyring.Encrypt(plainConfig)
		if err != nil {
			return rotated, fmt.Errorf("provider %s: %w", r.id, err)
		}

		newSecret := r.secret
		if r.secret.Valid && r.secret.String != "" {
			plainSecret, err := s.keyring.Decrypt(r.secret.String)
			if err != nil {
				return rotated, fmt.Errorf("provider %s webhook secret: %w", r.id, err)
			}
			sealed, err := s.keyring.Encrypt(plainSecret)
			if err != nil {
				return rotated, fmt.Errorf("provider %s webhook secret: %w", r.id, err)
			}
			newSecret = sql.NullString{String: sealed, Valid: true}
		}

		if _, err := db.ExecContext(ctx, rotateUpdateSecretsQuery, r.id, newConfig, newSecret); err != nil {
			return rotated, fmt.Errorf("provider %s: %w", r.id, err)
		}
		rotated++
	}

	return rotated, nil
}
