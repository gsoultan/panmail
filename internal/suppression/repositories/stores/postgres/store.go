package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_suppression.sql
	createSuppressionQuery string
	//go:embed sql/delete_suppression.sql
	deleteSuppressionQuery string
	//go:embed sql/get_suppression_by_email.sql
	getSuppressionByEmailQuery string
	//go:embed sql/list_suppressions.sql
	listSuppressionsQuery string
)

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) stores.SuppressionRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, sup *entities.Suppression) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createSuppressionQuery, sup.ID, sup.TenantID, sup.Email, sup.Reason, sup.CreatedAt)
	return err
}

func (s *store) Delete(ctx context.Context, tenantID, email string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteSuppressionQuery, tenantID, email)
	return err
}

func (s *store) GetByEmail(ctx context.Context, tenantID, email string) (*entities.Suppression, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	sup := &entities.Suppression{}
	err = db.QueryRowContext(ctx, getSuppressionByEmailQuery, tenantID, email).Scan(&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason, &sup.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return sup, nil
}

func (s *store) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Suppression, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listSuppressionsQuery), ";")
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", pageSize, offset)

	rows, err := dbConn.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var res []*entities.Suppression
	for rows.Next() {
		sup := &entities.Suppression{}
		err := rows.Scan(&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason, &sup.CreatedAt)
		if err != nil {
			return nil, "", err
		}
		res = append(res, sup)
	}

	nextPageToken := ""
	if len(res) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return res, nextPageToken, nil
}
