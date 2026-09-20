package usecases

import (
	"context"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

type ManageSuppressionsUsecase interface {
	Add(ctx context.Context, tenantID string, req *panmailv1.AddSuppressionRequest) (*panmailv1.Suppression, error)

	// Import writes a list in one pass. It never fails on an entry it cannot
	// use: the counts say what happened to each one.
	Import(ctx context.Context, tenantID string, entries []*panmailv1.SuppressionEntry) (ImportResult, error)
	Remove(ctx context.Context, tenantID, email string) error
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*panmailv1.Suppression, string, error)
	Check(ctx context.Context, tenantID, email string) (bool, string, error)
}
