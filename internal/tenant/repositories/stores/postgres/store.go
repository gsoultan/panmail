package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/internal/tenant/repositories"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_tenant.sql
	createTenantQuery string
	//go:embed sql/get_tenant_by_id.sql
	getTenantByIDQuery string
	//go:embed sql/list_tenants.sql
	listTenantsQuery string
	//go:embed sql/update_tenant.sql
	updateTenantQuery string
	//go:embed sql/delete_tenant.sql
	deleteTenantQuery string
)

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) repositories.TenantRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, t *entities.Tenant) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	retryPatternJSON, _ := json.Marshal(t.RetryPattern)
	_, err = dbConn.ExecContext(ctx, createTenantQuery, t.ID, t.Name, string(retryPatternJSON), t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *store) GetByID(ctx context.Context, id string) (*entities.Tenant, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	t := &entities.Tenant{}
	var retryPatternJSON sql.NullString
	err = dbConn.QueryRowContext(ctx, getTenantByIDQuery, id).Scan(&t.ID, &t.Name, &retryPatternJSON, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if retryPatternJSON.Valid {
		_ = json.Unmarshal([]byte(retryPatternJSON.String), &t.RetryPattern)
	}
	return t, nil
}

func (s *store) List(ctx context.Context, pageSize int, pageToken string) ([]*entities.Tenant, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listTenantsQuery), ";")
	query += " LIMIT $1 OFFSET $2"

	rows, err := dbConn.QueryContext(ctx, query, pageSize, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var tenants []*entities.Tenant
	for rows.Next() {
		t := &entities.Tenant{}
		var retryPatternJSON sql.NullString
		if err := rows.Scan(&t.ID, &t.Name, &retryPatternJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, "", err
		}
		if retryPatternJSON.Valid {
			_ = json.Unmarshal([]byte(retryPatternJSON.String), &t.RetryPattern)
		}
		tenants = append(tenants, t)
	}

	nextPageToken := ""
	if len(tenants) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return tenants, nextPageToken, nil
}

func (s *store) Update(ctx context.Context, t *entities.Tenant) error {
	dbConn, err := s.getDB()
	if err != nil {
		return err
	}
	retryPatternJSON, _ := json.Marshal(t.RetryPattern)
	_, err = dbConn.ExecContext(ctx, updateTenantQuery, t.ID, t.Name, string(retryPatternJSON), t.UpdatedAt)
	return err
}

func (s *store) Delete(ctx context.Context, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteTenantQuery, id)
	return err
}
