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

	// TruncateBefore removes received mail stored before the cutoff. Nothing
	// calls it unless an administrator has set an inbound retention: this
	// deletes the only copy panmail holds of a message someone was sent.
	TruncateBefore(ctx context.Context, before time.Time) (int64, error)

	Close() error
}
