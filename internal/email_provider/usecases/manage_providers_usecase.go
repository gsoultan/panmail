package usecases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type manageProvidersUsecase struct {
	repo    stores.Repository
	factory entities.ProviderFactory
}

func NewManageProvidersUsecase(repo stores.Repository, factory entities.ProviderFactory) ManageProvidersUsecase {
	return &manageProvidersUsecase{
		repo:    repo,
		factory: factory,
	}
}

func (u *manageProvidersUsecase) Create(ctx context.Context, tenantID string, req *panmailv1.CreateEmailProviderRequest) (*panmailv1.EmailProvider, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, err
	}

	// Check if name is unique for this tenant
	existing, _, _ := u.repo.List(ctx, tenantID, "", "", 1000, "")
	for _, p := range existing {
		if strings.EqualFold(p.Name, req.Name) {
			return nil, fmt.Errorf("an email provider with the name '%s' already exists for this tenant", req.Name)
		}
	}

	var config interface{}
	switch c := req.Config.(type) {
	case *panmailv1.CreateEmailProviderRequest_Smtp:
		config = c.Smtp
	case *panmailv1.CreateEmailProviderRequest_Imap:
		config = c.Imap
	case *panmailv1.CreateEmailProviderRequest_Pop3:
		config = c.Pop3
	}

	configBytes, err := protojson.Marshal(config.(proto.Message))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	p := &entities.EmailProvider{
		ID:             uuid.New().String(),
		TenantID:       tenantID,
		Name:           req.Name,
		Type:           req.Type,
		Config:         configBytes,
		AllowedDomains: req.AllowedDomains,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := u.repo.Create(ctx, p); err != nil {
		return nil, err
	}

	return u.toProto(p)
}

func (u *manageProvidersUsecase) Get(ctx context.Context, tenantID, id string) (*panmailv1.EmailProvider, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", id)
	}

	p, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return u.toProto(p)
}

func (u *manageProvidersUsecase) List(ctx context.Context, tenantID string, name string, providerType panmailv1.ProviderType, pageSize int, pageToken string) ([]*panmailv1.EmailProvider, string, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, "", err
	}
	typeStr := ""
	if providerType != panmailv1.ProviderType_PROVIDER_TYPE_UNSPECIFIED {
		typeStr = fmt.Sprintf("%d", int32(providerType))
	}
	providers, nextPageToken, err := u.repo.List(ctx, tenantID, name, typeStr, pageSize, pageToken)
	if err != nil {
		return nil, "", err
	}

	var res []*panmailv1.EmailProvider
	for _, p := range providers {
		proto, err := u.toProto(p)
		if err != nil {
			return nil, "", err
		}
		res = append(res, proto)
	}
	return res, nextPageToken, nil
}

func (u *manageProvidersUsecase) Update(ctx context.Context, tenantID string, req *panmailv1.UpdateEmailProviderRequest) (*panmailv1.EmailProvider, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(req.Id); err != nil {
		return nil, fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", req.Id)
	}

	// Check if name is unique for this tenant (if name changed)
	existing, _, _ := u.repo.List(ctx, tenantID, "", "", 1000, "")
	for _, p := range existing {
		if p.ID != req.Id && strings.EqualFold(p.Name, req.Name) {
			return nil, fmt.Errorf("an email provider with the name '%s' already exists for this tenant", req.Name)
		}
	}

	p, err := u.repo.GetByID(ctx, tenantID, req.Id)
	if err != nil {
		return nil, err
	}

	var config interface{}
	switch c := req.Config.(type) {
	case *panmailv1.UpdateEmailProviderRequest_Smtp:
		config = c.Smtp
	case *panmailv1.UpdateEmailProviderRequest_Imap:
		config = c.Imap
	case *panmailv1.UpdateEmailProviderRequest_Pop3:
		config = c.Pop3
	}

	if config != nil {
		configBytes, err := protojson.Marshal(config.(proto.Message))
		if err != nil {
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		p.Config = configBytes
	}

	p.Name = req.Name
	p.AllowedDomains = req.AllowedDomains
	p.UpdatedAt = time.Now()

	if err := u.repo.Update(ctx, p); err != nil {
		return nil, err
	}

	return u.toProto(p)
}

func (u *manageProvidersUsecase) Delete(ctx context.Context, tenantID, id string) error {
	if err := validateTenantID(tenantID); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", id)
	}
	return u.repo.Delete(ctx, tenantID, id)
}

func (u *manageProvidersUsecase) Test(ctx context.Context, tenantID, id string) error {
	if err := validateTenantID(tenantID); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", id)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	p, err := u.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("email provider not found")
	}

	provider, err := u.factory.CreateSender(p)
	if err != nil {
		// Try receiver if sender fails
		provider, err = u.factory.CreateReceiver(p)
		if err != nil {
			return err
		}
	}

	if provider == nil {
		return fmt.Errorf("failed to initialize email provider client")
	}

	return gsmail.Ping(ctx, provider)
}

func (u *manageProvidersUsecase) TestConfig(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var config interface{}
	switch c := req.Config.(type) {
	case *panmailv1.CreateEmailProviderRequest_Smtp:
		config = c.Smtp
	case *panmailv1.CreateEmailProviderRequest_Imap:
		config = c.Imap
	case *panmailv1.CreateEmailProviderRequest_Pop3:
		config = c.Pop3
	}

	if config == nil {
		return fmt.Errorf("provider configuration is required")
	}

	configBytes, err := protojson.Marshal(config.(proto.Message))
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	p := &entities.EmailProvider{
		Type:   req.Type,
		Config: configBytes,
	}

	provider, err := u.factory.CreateSender(p)
	if err != nil {
		// Try receiver if sender fails
		provider, err = u.factory.CreateReceiver(p)
		if err != nil {
			return err
		}
	}

	if provider == nil {
		return fmt.Errorf("failed to initialize email provider client")
	}

	return gsmail.Ping(ctx, provider)
}

func (u *manageProvidersUsecase) toProto(p *entities.EmailProvider) (*panmailv1.EmailProvider, error) {
	proto := &panmailv1.EmailProvider{
		Id:             p.ID,
		TenantId:       p.TenantID,
		Name:           p.Name,
		Type:           p.Type,
		AllowedDomains: p.AllowedDomains,
		CreateTime:     timestamppb.New(p.CreatedAt),
		UpdateTime:     timestamppb.New(p.UpdatedAt),
	}

	switch p.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
		c := &panmailv1.SmtpConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		proto.Config = &panmailv1.EmailProvider_Smtp{Smtp: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
		c := &panmailv1.ImapConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		proto.Config = &panmailv1.EmailProvider_Imap{Imap: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_POP3:
		c := &panmailv1.Pop3Config{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		proto.Config = &panmailv1.EmailProvider_Pop3{Pop3: c}
	}

	return proto, nil
}

func validateTenantID(tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant id is mandatory")
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return fmt.Errorf("invalid tenant id format: %s. tenant id must be a valid UUID", tenantID)
	}
	if tenantID == uuid.Nil.String() {
		return fmt.Errorf("tenant id cannot be nil uuid (00000000-0000-0000-0000-000000000000)")
	}
	return nil
}
