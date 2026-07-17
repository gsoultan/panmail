package usecases

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

var (
	testTenantID = uuid.New().String()
)

type mockRepo struct {
	providers map[string]*entities.EmailProvider
}

func (m *mockRepo) Create(ctx context.Context, p *entities.EmailProvider) error {
	m.providers[p.ID] = p
	return nil
}

func (m *mockRepo) GetByID(ctx context.Context, tenantID, id string) (*entities.EmailProvider, error) {
	p, ok := m.providers[id]
	if !ok || p.TenantID != tenantID {
		return nil, nil
	}
	return p, nil
}

func (m *mockRepo) List(ctx context.Context, tenantID string, name string, providerType string, pageSize int, pageToken string) ([]*entities.EmailProvider, string, error) {
	var res []*entities.EmailProvider
	for _, p := range m.providers {
		if p.TenantID == tenantID {
			res = append(res, p)
		}
	}
	return res, "", nil
}

func (m *mockRepo) Update(ctx context.Context, p *entities.EmailProvider) error {
	m.providers[p.ID] = p
	return nil
}

func (m *mockRepo) Delete(ctx context.Context, tenantID, id string) error {
	if p, ok := m.providers[id]; ok && p.TenantID == tenantID {
		delete(m.providers, id)
	}
	return nil
}

func TestManageProvidersUsecase_Create(t *testing.T) {
	repo := &mockRepo{providers: make(map[string]*entities.EmailProvider)}
	factory := NewProviderFactory()
	u := NewManageProvidersUsecase(repo, factory)

	tests := []struct {
		name    string
		req     *panmailv1.CreateEmailProviderRequest
		wantErr bool
	}{
		{
			name: "Create SMTP Provider",
			req: &panmailv1.CreateEmailProviderRequest{
				Name: "Test SMTP",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
				Config: &panmailv1.CreateEmailProviderRequest_Smtp{
					Smtp: &panmailv1.SmtpConfig{
						Host:     "smtp.gmail.com",
						Port:     587,
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Create IMAP Provider",
			req: &panmailv1.CreateEmailProviderRequest{
				Name: "Test IMAP",
				Type: panmailv1.ProviderType_PROVIDER_TYPE_IMAP,
				Config: &panmailv1.CreateEmailProviderRequest_Imap{
					Imap: &panmailv1.ImapConfig{
						Host:     "imap.gmail.com",
						Port:     993,
						Username: "user",
						Password: "pass",
					},
				},
			},
			wantErr: false,
		},
	}

	tenantID := testTenantID
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := u.Create(context.Background(), tenantID, tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Create() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil {
				if p.Name != tc.req.Name {
					t.Errorf("expected name %s, got %s", tc.req.Name, p.Name)
				}
				if p.Type != tc.req.Type {
					t.Errorf("expected type %v, got %v", tc.req.Type, p.Type)
				}

				// Verify config is correctly persisted and retrieved
				saved, _ := repo.GetByID(context.Background(), tenantID, p.Id)
				if saved == nil {
					t.Fatal("provider not saved in repo")
				}

				switch tc.req.Type {
				case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
					var cfg panmailv1.SmtpConfig
					if err := json.Unmarshal(saved.Config, &cfg); err != nil {
						t.Fatalf("failed to unmarshal saved config: %v", err)
					}
					if cfg.Host != tc.req.GetSmtp().Host {
						t.Errorf("expected host %s, got %s", tc.req.GetSmtp().Host, cfg.Host)
					}
				case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
					var cfg panmailv1.ImapConfig
					if err := json.Unmarshal(saved.Config, &cfg); err != nil {
						t.Fatalf("failed to unmarshal saved config: %v", err)
					}
					if cfg.Host != tc.req.GetImap().Host {
						t.Errorf("expected host %s, got %s", tc.req.GetImap().Host, cfg.Host)
					}
				}
			}
		})
	}
}

func TestManageProvidersUsecase_Update(t *testing.T) {
	repo := &mockRepo{providers: make(map[string]*entities.EmailProvider)}
	factory := NewProviderFactory()
	u := NewManageProvidersUsecase(repo, factory)
	tenantID := testTenantID

	// Pre-create a provider
	p, _ := u.Create(context.Background(), tenantID, &panmailv1.CreateEmailProviderRequest{
		Name: "Original SMTP",
		Type: panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		Config: &panmailv1.CreateEmailProviderRequest_Smtp{
			Smtp: &panmailv1.SmtpConfig{Host: "old.host"},
		},
	})

	updateReq := &panmailv1.UpdateEmailProviderRequest{
		Id:   p.Id,
		Name: "Updated SMTP",
		Config: &panmailv1.UpdateEmailProviderRequest_Smtp{
			Smtp: &panmailv1.SmtpConfig{Host: "new.host"},
		},
	}

	updated, err := u.Update(context.Background(), tenantID, updateReq)
	if err != nil {
		t.Fatalf("Update() failed: %v", err)
	}

	if updated.Name != "Updated SMTP" {
		t.Errorf("expected name Updated SMTP, got %s", updated.Name)
	}

	if updated.GetSmtp().Host != "new.host" {
		t.Errorf("expected host new.host, got %s", updated.GetSmtp().Host)
	}
}
