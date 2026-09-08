package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
	"github.com/gsoultan/panmail/pkg/db"
)

var (
	//go:embed sql/assign_membership.sql
	assignMembershipQuery string
	//go:embed sql/remove_membership.sql
	removeMembershipQuery string
	//go:embed sql/get_membership.sql
	getMembershipQuery string
	//go:embed sql/list_memberships_by_user.sql
	listMembershipsByUserQuery string
	//go:embed sql/update_membership_role.sql
	updateMembershipRoleQuery string
	//go:embed sql/delete_memberships_by_user.sql
	deleteMembershipsByUserQuery string
)

type membershipStore struct {
	conn db.Connection
}

func NewMembershipStore(conn db.Connection) repositories.MembershipRepository {
	return &membershipStore{conn: conn}
}

func (s *membershipStore) getDB() (*sql.DB, error) {
	if !s.conn.IsConnected() {
		return nil, errors.New("database not connected")
	}
	return s.conn.GetDB(), nil
}

func (s *membershipStore) Assign(ctx context.Context, m *entities.UserTenant) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, assignMembershipQuery,
		m.UserID, m.TenantID, m.Role, m.CreatedAt, m.UpdatedAt)
	return err
}

func (s *membershipStore) Remove(ctx context.Context, userID, tenantID string) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, removeMembershipQuery, userID, tenantID)
	return err
}

// Get reports the membership, or nil when there is none. A missing row is the
// ordinary answer to "may this user act here", so it is not an error.
func (s *membershipStore) Get(ctx context.Context, userID, tenantID string) (*entities.UserTenant, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	m := &entities.UserTenant{}
	err = conn.QueryRowContext(ctx, getMembershipQuery, userID, tenantID).
		Scan(&m.UserID, &m.TenantID, &m.Role, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *membershipStore) ListByUser(ctx context.Context, userID string) ([]*entities.UserTenant, error) {
	conn, err := s.getDB()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, listMembershipsByUserQuery, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memberships []*entities.UserTenant
	for rows.Next() {
		m := &entities.UserTenant{}
		if err := rows.Scan(&m.UserID, &m.TenantID, &m.TenantName, &m.Role, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		memberships = append(memberships, m)
	}
	return memberships, rows.Err()
}

func (s *membershipStore) UpdateRole(ctx context.Context, userID, tenantID, role string) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, updateMembershipRoleQuery, role, time.Now(), userID, tenantID)
	return err
}

func (s *membershipStore) RemoveAllForUser(ctx context.Context, userID string) error {
	conn, err := s.getDB()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, deleteMembershipsByUserQuery, userID)
	return err
}
