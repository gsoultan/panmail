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

	configBytes, err := marshalCreateConfig(req)
	if err != nil {
		return nil, err
	}

	p := &entities.EmailProvider{
		ID:             uuid.New().String(),
		TenantID:       tenantID,
		Name:           req.Name,
		Type:           req.Type,
		Config:         configBytes,
		AllowedDomains: req.AllowedDomains,
		WebhookSecret:  req.WebhookSecret,
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

	config := updateConfigMessage(req)

	if config != nil {
		configBytes, err := protojson.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		// Reads redact the password, so a client round-tripping a provider
		// sends an empty one back. Treat that as "unchanged" rather than
		// wiping the credential the provider needs to work.
		configBytes, err = preserveStoredPassword(p.Type, p.Config, configBytes)
		if err != nil {
			return nil, err
		}
		p.Config = configBytes
	}

	p.Name = req.Name
	p.AllowedDomains = req.AllowedDomains
	p.UpdatedAt = time.Now()

	// An empty secret means "leave it alone": the API never returns the stored
	// value, so a client editing a provider has nothing to send back.
	if req.WebhookSecret != "" {
		p.WebhookSecret = req.WebhookSecret
	}

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

	provider, err := u.dialProvider(p)
	if err != nil {
		return err
	}

	return gsmail.Ping(ctx, provider)
}

func (u *manageProvidersUsecase) TestConfig(ctx context.Context, req *panmailv1.CreateEmailProviderRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	configBytes, err := marshalCreateConfig(req)
	if err != nil {
		return err
	}

	p := &entities.EmailProvider{
		Type:   req.Type,
		Config: configBytes,
	}

	provider, err := u.dialProvider(p)
	if err != nil {
		return err
	}

	return gsmail.Ping(ctx, provider)
}

// redactedPassword is what callers see in place of a stored credential. It is
// a fixed marker rather than the real length, which would leak information.
const redactedPassword = ""

// toProto maps a provider for the API.
//
// Credentials never leave the server: the password on every transport config
// and the webhook secret are cleared. Reading a provider is a Viewer-level
// action, so returning them made every SMTP password readable by the least
// privileged role in the tenant.
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
		c.Password = redactedPassword
		// The DKIM private key is a credential too, and a leaked one lets
		// anyone sign mail as this domain. The domain and selector are public —
		// they are published in DNS — so only the key is withheld.
		if c.Dkim != nil {
			c.Dkim.PrivateKey = redactedPassword
		}
		// The client id, endpoint, scope and mechanism are configuration the
		// operator needs to see in order to edit; the secret and the refresh
		// token are credentials.
		if c.Oauth2 != nil {
			c.Oauth2.ClientSecret = redactedPassword
			c.Oauth2.RefreshToken = redactedPassword
		}
		proto.Config = &panmailv1.EmailProvider_Smtp{Smtp: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
		c := &panmailv1.ImapConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		c.Password = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Imap{Imap: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_POP3:
		c := &panmailv1.Pop3Config{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		c.Password = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Pop3{Pop3: c}

	// The API providers. Each hides its one secret; the rest of the config —
	// region, domain, stream, endpoint — is operational detail the operator
	// needs to see in order to edit it.
	case panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID:
		c := &panmailv1.SendGridConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		c.ApiKey = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Sendgrid{Sendgrid: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_SES:
		c := &panmailv1.SesConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		// The access key identifies the credential and is not itself secret;
		// the secret key is.
		c.SecretKey = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Ses{Ses: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK:
		c := &panmailv1.PostmarkConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		c.ServerToken = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Postmark{Postmark: c}
	case panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN:
		c := &panmailv1.MailgunConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		c.ApiKey = redactedPassword
		proto.Config = &panmailv1.EmailProvider_Mailgun{Mailgun: c}
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

// dialProvider builds whichever client the provider type supports, so that a
// connectivity test works for senders and receivers alike.
// dialProvider returns something that can be health-checked.
//
// The return type is gsmail.Pinger rather than any: the only caller passes the
// result straight to gsmail.Ping, and every sender and receiver the factory
// builds implements it. Returning any meant a provider that could not be pinged
// would have failed at the call site instead of here.
func (u *manageProvidersUsecase) dialProvider(p *entities.EmailProvider) (gsmail.Pinger, error) {
	if sender, err := u.factory.CreateSender(p); err == nil {
		if pinger, ok := sender.(gsmail.Pinger); ok {
			return pinger, nil
		}
		return nil, fmt.Errorf("this provider type cannot be health-checked")
	}

	receiver, err := u.factory.CreateReceiver(p)
	if err != nil {
		return nil, err
	}
	pinger, ok := receiver.(gsmail.Pinger)
	if !ok {
		return nil, fmt.Errorf("this provider type cannot be health-checked")
	}
	return pinger, nil
}

// createConfigMessage and updateConfigMessage pull the populated arm out of the
// request's config oneof.
//
// These exist as one function each because the same switch used to be written
// out three times — in Create, Update and TestConfig — and a provider type
// added to two of them would marshal a nil config in the third, storing an
// empty configuration with no error. One place to add a case is one place to
// forget it.
func createConfigMessage(req *panmailv1.CreateEmailProviderRequest) proto.Message {
	switch c := req.Config.(type) {
	case *panmailv1.CreateEmailProviderRequest_Smtp:
		return c.Smtp
	case *panmailv1.CreateEmailProviderRequest_Imap:
		return c.Imap
	case *panmailv1.CreateEmailProviderRequest_Pop3:
		return c.Pop3
	case *panmailv1.CreateEmailProviderRequest_Sendgrid:
		return c.Sendgrid
	case *panmailv1.CreateEmailProviderRequest_Ses:
		return c.Ses
	case *panmailv1.CreateEmailProviderRequest_Postmark:
		return c.Postmark
	case *panmailv1.CreateEmailProviderRequest_Mailgun:
		return c.Mailgun
	default:
		return nil
	}
}

func updateConfigMessage(req *panmailv1.UpdateEmailProviderRequest) proto.Message {
	switch c := req.Config.(type) {
	case *panmailv1.UpdateEmailProviderRequest_Smtp:
		return c.Smtp
	case *panmailv1.UpdateEmailProviderRequest_Imap:
		return c.Imap
	case *panmailv1.UpdateEmailProviderRequest_Pop3:
		return c.Pop3
	case *panmailv1.UpdateEmailProviderRequest_Sendgrid:
		return c.Sendgrid
	case *panmailv1.UpdateEmailProviderRequest_Ses:
		return c.Ses
	case *panmailv1.UpdateEmailProviderRequest_Postmark:
		return c.Postmark
	case *panmailv1.UpdateEmailProviderRequest_Mailgun:
		return c.Mailgun
	default:
		return nil
	}
}

// marshalCreateConfig refuses a request with no configuration rather than
// marshalling a nil message, which previously panicked on the type assertion.
func marshalCreateConfig(req *panmailv1.CreateEmailProviderRequest) ([]byte, error) {
	config := createConfigMessage(req)
	if config == nil {
		return nil, fmt.Errorf("provider configuration is required")
	}
	b, err := protojson.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}
	return b, nil
}
