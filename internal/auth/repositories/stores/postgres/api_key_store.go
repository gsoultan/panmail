package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_api_key.sql
	createApiKeyQuery string
	//go:embed sql/list_api_keys.sql
	listApiKeysQuery string
	//go:embed sql/delete_api_key.sql
	deleteApiKeyQuery string
	//go:embed sql/get_api_key_by_hash.sql
	getApiKeyByHashQuery string
	//go:embed sql/get_api_key_by_id.sql
	getApiKeyByIDQuery string
	//go:embed sql/update_api_key_status.sql
	updateApiKeyStatusQuery string
	//go:embed sql/update_api_key_last_used.sql
	updateApiKeyLastUsedQuery string
)

type apiKeyStore struct {
	conn db.Connection
}

func NewApiKeyStore(conn db.Connection) repositories.ApiKeyRepository {
	return &apiKeyStore{conn: conn}
}

func (s *apiKeyStore) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *apiKeyStore) Create(ctx context.Context, key *entities.ApiKey) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createApiKeyQuery, key.ID, key.TenantID, key.Name, key.KeyHash, key.Prefix, key.ExpiresAt, key.IsEnabled, key.CreatedAt, key.UpdatedAt)
	return err
}

func (s *apiKeyStore) ListByTenantID(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.ApiKey, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listApiKeysQuery), ";")
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", pageSize, offset)

	rows, err := dbConn.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var keys []*entities.ApiKey
	for rows.Next() {
		k := &entities.ApiKey{}
		var lastUsedAt, expiresAt sql.NullTime
		err := rows.Scan(&k.ID, &k.TenantID, &k.Name, &k.KeyHash, &k.Prefix, &lastUsedAt, &expiresAt, &k.IsEnabled, &k.CreatedAt, &k.UpdatedAt)
		if err != nil {
			return nil, "", err
		}
		if lastUsedAt.Valid {
			k.LastUsedAt = &lastUsedAt.Time
		}
		if expiresAt.Valid {
			k.ExpiresAt = &expiresAt.Time
		}
		keys = append(keys, k)
	}

	nextPageToken := ""
	if len(keys) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return keys, nextPageToken, nil
}

func (s *apiKeyStore) Delete(ctx context.Context, id string, tenantID string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteApiKeyQuery, id, tenantID)
	return err
}

func (s *apiKeyStore) GetByHash(ctx context.Context, hash string) (*entities.ApiKey, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	k := &entities.ApiKey{}
	var lastUsedAt, expiresAt sql.NullTime
	err = db.QueryRowContext(ctx, getApiKeyByHashQuery, hash).Scan(&k.ID, &k.TenantID, &k.Name, &k.KeyHash, &k.Prefix, &lastUsedAt, &expiresAt, &k.IsEnabled, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.Time
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	return k, nil
}

func (s *apiKeyStore) GetByID(ctx context.Context, id string, tenantID string) (*entities.ApiKey, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	k := &entities.ApiKey{}
	var lastUsedAt, expiresAt sql.NullTime
	err = db.QueryRowContext(ctx, getApiKeyByIDQuery, id, tenantID).Scan(&k.ID, &k.TenantID, &k.Name, &k.KeyHash, &k.Prefix, &lastUsedAt, &expiresAt, &k.IsEnabled, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		k.LastUsedAt = &lastUsedAt.Time
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	return k, nil
}

func (s *apiKeyStore) UpdateStatus(ctx context.Context, id string, tenantID string, isEnabled bool) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, updateApiKeyStatusQuery, isEnabled, time.Now(), id, tenantID)
	return err
}

func (s *apiKeyStore) UpdateLastUsed(ctx context.Context, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = db.ExecContext(ctx, updateApiKeyLastUsedQuery, id, now)
	return err
}
