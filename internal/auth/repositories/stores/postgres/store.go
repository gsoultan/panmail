package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strings"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/create_user.sql
	createUserQuery string
	//go:embed sql/get_user_by_email.sql
	getUserByEmailQuery string
	//go:embed sql/get_user_by_id.sql
	getUserByIDQuery string
	//go:embed sql/list_users_by_tenant.sql
	listUsersByTenantQuery string
	//go:embed sql/update_user_role.sql
	updateUserRoleQuery string
	//go:embed sql/update_user_two_factor.sql
	updateUserTwoFactorQuery string
	//go:embed sql/delete_user.sql
	deleteUserQuery string
	//go:embed sql/count_users.sql
	countUsersQuery string
)

type store struct {
	conn db.Connection
}

func NewStore(conn db.Connection) repositories.UserRepository {
	return &store{conn: conn}
}

func (s *store) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *store) Create(ctx context.Context, u *entities.User) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createUserQuery, u.ID, u.TenantID, u.Email, u.Password, u.Name, u.Role, u.TwoFactorEnabled, u.TwoFactorSecret, u.CreatedAt, u.UpdatedAt)
	return err
}

func (s *store) GetByEmail(ctx context.Context, email string) (*entities.User, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	u := &entities.User{}
	var tenantID sql.NullString
	var twoFactorSecret sql.NullString
	err = db.QueryRowContext(ctx, getUserByEmailQuery, email).Scan(&u.ID, &tenantID, &u.Email, &u.Password, &u.Name, &u.Role, &u.TwoFactorEnabled, &twoFactorSecret, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.TenantID = tenantID.String
	u.TwoFactorSecret = twoFactorSecret.String
	return u, nil
}

func (s *store) GetByID(ctx context.Context, id string) (*entities.User, error) {
	db, err := s.getDB()
	if err != nil {
		return nil, err
	}
	u := &entities.User{}
	var tenantID sql.NullString
	var twoFactorSecret sql.NullString
	err = db.QueryRowContext(ctx, getUserByIDQuery, id).Scan(&u.ID, &tenantID, &u.Email, &u.Password, &u.Name, &u.Role, &u.TwoFactorEnabled, &twoFactorSecret, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.TenantID = tenantID.String
	u.TwoFactorSecret = twoFactorSecret.String
	return u, nil
}

func (s *store) ListByTenantID(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.User, string, error) {
	dbConn, err := s.getDB()
	if err != nil {
		return nil, "", err
	}

	offset := db.DecodeOffset(pageToken)
	if pageSize <= 0 {
		pageSize = 20
	}

	query := strings.TrimSuffix(strings.TrimSpace(listUsersByTenantQuery), ";")
	query += " LIMIT $2 OFFSET $3"

	rows, err := dbConn.QueryContext(ctx, query, tenantID, pageSize, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var users []*entities.User
	for rows.Next() {
		u := &entities.User{}
		var tID sql.NullString
		var twoFactorSecret sql.NullString
		err := rows.Scan(&u.ID, &tID, &u.Email, &u.Password, &u.Name, &u.Role, &u.TwoFactorEnabled, &twoFactorSecret, &u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			return nil, "", err
		}
		u.TenantID = tID.String
		u.TwoFactorSecret = twoFactorSecret.String
		users = append(users, u)
	}

	nextPageToken := ""
	if len(users) == pageSize {
		nextPageToken = db.EncodeOffset(offset + pageSize)
	}

	return users, nextPageToken, nil
}

func (s *store) UpdateRole(ctx context.Context, id string, role string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, updateUserRoleQuery, role, time.Now(), id)
	return err
}

func (s *store) UpdateTwoFactor(ctx context.Context, id string, enabled bool, secret string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, updateUserTwoFactorQuery, enabled, secret, time.Now(), id)
	return err
}

func (s *store) Delete(ctx context.Context, id string) error {
	db, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, deleteUserQuery, id)
	return err
}

func (s *store) Count(ctx context.Context) (int, error) {
	db, err := s.getDB()
	if err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRowContext(ctx, countUsersQuery).Scan(&count)
	return count, err
}
