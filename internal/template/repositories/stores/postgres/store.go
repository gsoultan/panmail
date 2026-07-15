package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/panmail/internal/template/repositories/entities"
	"github.com/gsoultan/panmail/internal/template/repositories/stores"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_template.sql
	createTemplateQuery string
	//go:embed sql/get_template_by_id.sql
	getTemplateByIDQuery string
	//go:embed sql/list_templates.sql
	listTemplatesQuery string
	//go:embed sql/update_template.sql
	updateTemplateQuery string
	//go:embed sql/delete_template.sql
	deleteTemplateQuery string
)

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) stores.TemplateRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, t *entities.Template) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createTemplateQuery, t.ID, t.TenantID, t.Name, t.Subject, t.BodyHTML, t.BodyText, t.Design, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *store) GetByID(ctx context.Context, tenantID, id string) (*entities.Template, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	t := &entities.Template{}
	var design sql.NullString
	err = db.QueryRowContext(ctx, getTemplateByIDQuery, tenantID, id).Scan(&t.ID, &t.TenantID, &t.Name, &t.Subject, &t.BodyHTML, &t.BodyText, &design, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.Design = design.String
	return t, nil
}

func (s *store) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.Template, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listTemplatesQuery), ";")
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", pageSize, offset)

	rows, err := dbConn.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var res []*entities.Template
	for rows.Next() {
		t := &entities.Template{}
		var design sql.NullString
		err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Subject, &t.BodyHTML, &t.BodyText, &design, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, "", err
		}
		t.Design = design.String
		res = append(res, t)
	}

	nextPageToken := ""
	if len(res) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return res, nextPageToken, nil
}

func (s *store) Update(ctx context.Context, t *entities.Template) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, updateTemplateQuery, t.TenantID, t.ID, t.Name, t.Subject, t.BodyHTML, t.BodyText, t.Design, t.UpdatedAt)
	return err
}

func (s *store) Delete(ctx context.Context, tenantID, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteTemplateQuery, tenantID, id)
	return err
}
