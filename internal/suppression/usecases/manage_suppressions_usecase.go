package usecases

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/suppression/repositories/entities"
	"github.com/gsoultan/panmail/internal/suppression/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A suppression list only works if the same mailbox always produces the same
// key. "Alice <Alice@Example.COM>" and "alice@example.com" are one mailbox, and
// storing them as two entries means a bounce recorded under one spelling does
// not stop the next send to the other — the list silently fails open, which is
// the one way it must never fail.
//
// gsmail.NormalizeAddress reduces an address to the bare lowercased addr-spec,
// which is exactly the key a suppression list should use. Every write and every
// read goes through here.
func suppressionKey(email string) string {
	return gsmail.NormalizeAddress(email)
}

type manageSuppressionsUsecase struct {
	repo stores.SuppressionRepository
}

func NewManageSuppressionsUsecase(repo stores.SuppressionRepository) ManageSuppressionsUsecase {
	return &manageSuppressionsUsecase{
		repo: repo,
	}
}

func (u *manageSuppressionsUsecase) Add(ctx context.Context, tenantID string, req *panmailv1.AddSuppressionRequest) (*panmailv1.Suppression, error) {
	s := &entities.Suppression{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     suppressionKey(req.Email),
		Reason:    req.Reason,
		CreatedAt: time.Now(),
	}

	if err := u.repo.Create(ctx, s); err != nil {
		return nil, err
	}

	return u.toProto(s), nil
}

func (u *manageSuppressionsUsecase) Remove(ctx context.Context, tenantID, email string) error {
	email = suppressionKey(email)
	return u.repo.Delete(ctx, tenantID, email)
}

func (u *manageSuppressionsUsecase) List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Suppression, string, error) {
	sups, nextToken, err := u.repo.List(ctx, tenantID, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	res := make([]*panmailv1.Suppression, len(sups))
	for i, s := range sups {
		res[i] = u.toProto(s)
	}
	return res, nextToken, nil
}

func (u *manageSuppressionsUsecase) Check(ctx context.Context, tenantID, email string) (bool, string, error) {
	s, err := u.repo.GetByEmail(ctx, tenantID, suppressionKey(email))
	if err != nil {
		return false, "", err
	}
	if s == nil {
		return false, "", nil
	}
	return true, s.Reason, nil
}

func (u *manageSuppressionsUsecase) toProto(s *entities.Suppression) *panmailv1.Suppression {
	return &panmailv1.Suppression{
		Id:         s.ID,
		TenantId:   s.TenantID,
		Email:      s.Email,
		Reason:     s.Reason,
		CreateTime: timestamppb.New(s.CreatedAt),
	}
}
