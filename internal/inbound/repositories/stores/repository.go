package stores

import (
	"context"
	"time"

	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
)

type InboundRepository interface {
	Write(ctx context.Context, email *entities.InboundEmail) error
	List(ctx context.Context, tenantID string, pageSize int, pageToken string) ([]*entities.InboundEmail, string, error)
	GetByID(ctx context.Context, tenantID, id string) (*entities.InboundEmail, error)
	Count(ctx context.Context, tenantID string, startTime, endTime time.Time) (int64, error)
	Close() error
}
