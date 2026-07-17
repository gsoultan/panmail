package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	inboundUsecases "github.com/gsoultan/panmail/internal/inbound/usecases"
	tenantRepositories "github.com/gsoultan/panmail/internal/tenant/repositories"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Poller struct {
	tenantRepo      tenantRepositories.TenantRepository
	providerRepo    providerStores.Repository
	inboundUsecase  inboundUsecases.InboundUsecase
	providerFactory providerEntities.ProviderFactory
	interval        time.Duration
}

func NewPoller(
	tenantRepo tenantRepositories.TenantRepository,
	providerRepo providerStores.Repository,
	inboundUsecase inboundUsecases.InboundUsecase,
	providerFactory providerEntities.ProviderFactory,
	interval time.Duration,
) *Poller {
	return &Poller{
		tenantRepo:      tenantRepo,
		providerRepo:    providerRepo,
		inboundUsecase:  inboundUsecase,
		providerFactory: providerFactory,
		interval:        interval,
	}
}

func (p *Poller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollAll(ctx)
		}
	}
}

func (p *Poller) pollAll(ctx context.Context) {
	tenants, _, err := p.tenantRepo.List(ctx, 1000, "")
	if err != nil {
		slog.Error("poller: failed to list tenants", "error", err)
		return
	}

	var wg sync.WaitGroup
	// Limit concurrency for provider polling to handle high load
	sem := make(chan struct{}, 50)

	for _, tenant := range tenants {
		providers, _, err := p.providerRepo.List(ctx, tenant.ID, "", "", 1000, "")
		if err != nil {
			slog.Error("poller: failed to list providers", "tenant_id", tenant.ID, "error", err)
			continue
		}

		for _, provider := range providers {
			if provider.Type != panmailv1.ProviderType_PROVIDER_TYPE_IMAP && provider.Type != panmailv1.ProviderType_PROVIDER_TYPE_POP3 {
				continue
			}

			wg.Add(1)
			go func(tID string, prov *providerEntities.EmailProvider) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
					if err := p.pollProvider(ctx, tID, prov); err != nil {
						slog.Error("poller: failed to poll provider", "tenant_id", tID, "provider_id", prov.ID, "error", err)
					}
				case <-ctx.Done():
					return
				}
			}(tenant.ID, provider)
		}
	}
	wg.Wait()
}

func (p *Poller) pollProvider(ctx context.Context, tenantID string, provider *providerEntities.EmailProvider) error {
	receiverObj, err := p.providerFactory.CreateReceiver(provider)
	if err != nil {
		return err
	}

	receiver, ok := receiverObj.(gsmail.Receiver)
	if !ok {
		return fmt.Errorf("provider is not a receiver")
	}

	// We'll pull the last 10 messages for now.
	// In a real implementation, we'd use a cursor (last UID seen).
	emails, err := receiver.Receive(ctx, 10)
	if err != nil {
		return err
	}

	for _, e := range emails {
		inbound := &panmailv1.InboundEmail{
			Id:        uuid.New().String(),
			TenantId:  tenantID,
			From:      e.From,
			To:        e.To,
			Subject:   e.Subject,
			BodyHtml:  string(e.HTMLBody),
			BodyText:  string(e.Body),
			Timestamp: timestamppb.New(time.Now()), // Ideally use email date header
			Headers:   make(map[string]string),
		}

		if err := p.inboundUsecase.Process(ctx, inbound); err != nil {
			slog.Error("poller: failed to process inbound email", "error", err)
		}
	}

	return nil
}
