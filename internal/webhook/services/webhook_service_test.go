package services

import (
	"context"
	"net/netip"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

type memWebhooks struct {
	stores.WebhookRepository
	saved []*entities.Webhook
}

func (m *memWebhooks) Create(_ context.Context, w *entities.Webhook) error {
	m.saved = append(m.saved, w)
	return nil
}

func (m *memWebhooks) Update(_ context.Context, w *entities.Webhook) error {
	m.saved = append(m.saved, w)
	return nil
}

func (m *memWebhooks) GetByID(_ context.Context, _, id string) (*entities.Webhook, error) {
	return &entities.Webhook{ID: id}, nil
}

type staticResolver map[string]netip.Addr

func (s staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return []netip.Addr{s[host]}, nil
}

var resolver = staticResolver{
	"public.example":   netip.MustParseAddr("93.184.216.34"),
	"intranet.example": netip.MustParseAddr("192.168.10.20"),
}

// With the operator's restriction on, saving a subscription aimed at a private
// address is the tenant's mistake to correct, and is refused as one -- not as
// a server fault, and not by queueing deliveries that will all fail.
func TestAPrivateTargetIsRefusedOnSaveWhenRestricted(t *testing.T) {
	repo := &memWebhooks{}
	svc := NewWebhookService(usecases.NewWebhookUsecase(repo, usecases.RestrictTargets(resolver)))
	ctx := context.Background()

	for _, url := range []string{"https://intranet.example/hook", "http://127.0.0.1:9000/", "http://[fd00::1]/"} {
		_, err := svc.CreateWebhook(ctx, connect.NewRequest(&panmailv1.CreateWebhookRequest{Name: "n", Url: url}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("create %s: %v, want InvalidArgument", url, err)
		}
		_, err = svc.UpdateWebhook(ctx, connect.NewRequest(&panmailv1.UpdateWebhookRequest{Id: "x", Name: "n", Url: url}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("update %s: %v, want InvalidArgument", url, err)
		}
	}
	if len(repo.saved) != 0 {
		t.Errorf("%d refused subscriptions were saved anyway", len(repo.saved))
	}

	if _, err := svc.CreateWebhook(ctx, connect.NewRequest(&panmailv1.CreateWebhookRequest{Name: "n", Url: "https://public.example/hook"})); err != nil {
		t.Errorf("a public target was refused: %v", err)
	}
}

func TestAPrivateTargetIsAcceptedByDefault(t *testing.T) {
	repo := &memWebhooks{}
	svc := NewWebhookService(usecases.NewWebhookUsecase(repo))
	_, err := svc.CreateWebhook(context.Background(), connect.NewRequest(&panmailv1.CreateWebhookRequest{Name: "n", Url: "http://192.168.1.10/hook"}))
	if err != nil {
		t.Errorf("a self-hosted target was refused with no restriction configured: %v", err)
	}
}
