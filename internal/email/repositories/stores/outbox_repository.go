package stores

import (
	"context"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
)

type OutboxRepository interface {
	Create(ctx context.Context, email *entities.OutboxEmail) error
	GetByID(ctx context.Context, id string) (*entities.OutboxEmail, error)
	ListPending(ctx context.Context, limit int) ([]*entities.OutboxEmail, error)
	Update(ctx context.Context, email *entities.OutboxEmail) error
	Delete(ctx context.Context, id string) error
}
