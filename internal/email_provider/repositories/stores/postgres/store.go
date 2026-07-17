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
)

var (
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

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) stores.Repository {
	return &store{conn: conn}
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
	allowedDomainsJSON, _ := json.Marshal(p.AllowedDomains)
	_, err = db.ExecContext(ctx, createProviderQuery, p.ID, p.TenantID, p.Name, p.Type, string(p.Config), string(allowedDomainsJSON), p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *store) GetByID(ctx context.Context, tenantID, id string) (*entities.EmailProvider, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	p := &entities.EmailProvider{}
	var allowedDomainsJSON []byte
	err = db.QueryRowContext(ctx, getProviderByIDQuery, tenantID, id).Scan(&p.ID, &p.TenantID, &p.Name, &p.Type, &p.Config, &allowedDomainsJSON, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(allowedDomainsJSON) > 0 {
		_ = json.Unmarshal(allowedDomainsJSON, &p.AllowedDomains)
	}
	return p, nil
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

	query := "SELECT id, tenant_id, name, type, config, allowed_domains, created_at, updated_at FROM email_providers WHERE tenant_id = $1"
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
		p := &entities.EmailProvider{}
		var allowedDomainsJSON []byte
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.Type, &p.Config, &allowedDomainsJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, "", err
		}
		if len(allowedDomainsJSON) > 0 {
			_ = json.Unmarshal(allowedDomainsJSON, &p.AllowedDomains)
		}
		providers = append(providers, p)
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
	allowedDomainsJSON, _ := json.Marshal(p.AllowedDomains)
	_, err = db.ExecContext(ctx, updateProviderQuery, p.TenantID, p.ID, p.Name, string(p.Config), string(allowedDomainsJSON), p.UpdatedAt)
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
