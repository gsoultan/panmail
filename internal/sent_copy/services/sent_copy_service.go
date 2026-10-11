// Package services exposes the Sent copies setting over Connect.
//
// Tenant administrators only, enforced by the RBAC interceptor from the
// procedure table in internal/auth/middlewares/policy.go. The tenant is always
// the one the caller is acting in, never one named in the request.
package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/usecases"
)

type sentCopyService struct {
	usecase *usecases.Settings
}

// NewSentCopyService builds the service.
func NewSentCopyService(usecase *usecases.Settings) panmailv1connect.SentCopyServiceHandler {
	return &sentCopyService{usecase: usecase}
}

func (s *sentCopyService) GetSentCopySettings(ctx context.Context, _ *connect.Request[panmailv1.GetSentCopySettingsRequest]) (*connect.Response[panmailv1.GetSentCopySettingsResponse], error) {
	set, err := s.usecase.Get(ctx, authmiddlewares.GetTenantID(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.GetSentCopySettingsResponse{
		Settings:            settingsToProto(set),
		EncryptionAvailable: s.usecase.EncryptionAvailable(),
	}), nil
}

func (s *sentCopyService) UpdateSentCopySettings(ctx context.Context, req *connect.Request[panmailv1.UpdateSentCopySettingsRequest]) (*connect.Response[panmailv1.UpdateSentCopySettingsResponse], error) {
	in := req.Msg.GetSettings()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("settings are required"))
	}
	tenantID := authmiddlewares.GetTenantID(ctx)
	set, err := s.usecase.Update(ctx, tenantID, settingsFromProto(in))
	if err != nil {
		return nil, mapError(err)
	}
	slog.Info("sent copies setting changed", "tenant_id", tenantID,
		"user_id", userID(ctx), "mode", string(set.Mode))
	return connect.NewResponse(&panmailv1.UpdateSentCopySettingsResponse{Settings: settingsToProto(set)}), nil
}

func (s *sentCopyService) ListSentCopyMailboxes(ctx context.Context, _ *connect.Request[panmailv1.ListSentCopyMailboxesRequest]) (*connect.Response[panmailv1.ListSentCopyMailboxesResponse], error) {
	list, err := s.usecase.ListMailboxes(ctx, authmiddlewares.GetTenantID(ctx))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*panmailv1.SentCopyMailbox, 0, len(list))
	for _, m := range list {
		out = append(out, mailboxToProto(m))
	}
	return connect.NewResponse(&panmailv1.ListSentCopyMailboxesResponse{Mailboxes: out}), nil
}

func (s *sentCopyService) SaveSentCopyMailbox(ctx context.Context, req *connect.Request[panmailv1.SaveSentCopyMailboxRequest]) (*connect.Response[panmailv1.SaveSentCopyMailboxResponse], error) {
	in := req.Msg.GetMailbox()
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a mailbox is required"))
	}
	tenantID := authmiddlewares.GetTenantID(ctx)
	m, err := s.usecase.SaveMailbox(ctx, tenantID, &entities.Mailbox{
		Address: in.GetAddress(), Username: in.GetUsername(), Password: in.GetPassword(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	slog.Info("sent copies mailbox saved", "tenant_id", tenantID,
		"user_id", userID(ctx), "address", m.Address)
	return connect.NewResponse(&panmailv1.SaveSentCopyMailboxResponse{Mailbox: mailboxToProto(m)}), nil
}

func (s *sentCopyService) DeleteSentCopyMailbox(ctx context.Context, req *connect.Request[panmailv1.DeleteSentCopyMailboxRequest]) (*connect.Response[panmailv1.DeleteSentCopyMailboxResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	if err := s.usecase.DeleteMailbox(ctx, tenantID, req.Msg.GetAddress()); err != nil {
		return nil, mapError(err)
	}
	slog.Info("sent copies mailbox deleted", "tenant_id", tenantID,
		"user_id", userID(ctx), "address", req.Msg.GetAddress())
	return connect.NewResponse(&panmailv1.DeleteSentCopyMailboxResponse{}), nil
}

// mapError gives a validation failure InvalidArgument, a missing data key
// FailedPrecondition and anything else Internal.
func mapError(err error) error {
	switch {
	case errors.Is(err, usecases.ErrEncryptionUnavailable):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, usecases.ErrMailboxNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	}
	for _, v := range []error{
		entities.ErrInvalidMode, entities.ErrHostRequired, entities.ErrInvalidHost, entities.ErrInvalidPort,
		entities.ErrMasterRequired, entities.ErrInvalidFolder, entities.ErrInvalidSeparator,
		entities.ErrAddressRequired, entities.ErrPasswordRequired, entities.ErrInvalidCredentials,
	} {
		if errors.Is(err, v) {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	return connect.NewError(connect.CodeInternal, err)
}

var modeToProto = map[entities.Mode]panmailv1.SentCopyMode{
	entities.ModeNone:         panmailv1.SentCopyMode_SENT_COPY_MODE_NONE,
	entities.ModeSharedLogin:  panmailv1.SentCopyMode_SENT_COPY_MODE_SHARED_LOGIN,
	entities.ModePerUserLogin: panmailv1.SentCopyMode_SENT_COPY_MODE_PER_USER_LOGIN,
	entities.ModeBccSender:    panmailv1.SentCopyMode_SENT_COPY_MODE_BCC_SENDER,
}

func modeFromProto(m panmailv1.SentCopyMode) entities.Mode {
	for k, v := range modeToProto {
		if v == m {
			return k
		}
	}
	if m == panmailv1.SentCopyMode_SENT_COPY_MODE_UNSPECIFIED {
		return entities.ModeNone
	}
	// Unknown values pass through as themselves and fail validation.
	return entities.Mode(m.String())
}

func settingsFromProto(in *panmailv1.SentCopySettings) *entities.Settings {
	return &entities.Settings{
		Mode:            modeFromProto(in.GetMode()),
		IMAPHost:        in.GetImapHost(),
		IMAPPort:        int(in.GetImapPort()),
		IMAPUseSSL:      in.GetImapUseSsl(),
		SentFolder:      in.GetSentFolder(),
		MasterUsername:  in.GetMasterUsername(),
		MasterPassword:  in.GetMasterPassword(),
		MasterSeparator: in.GetMasterSeparator(),
	}
}

// settingsToProto never returns the password, only whether one is stored.
func settingsToProto(s *entities.Settings) *panmailv1.SentCopySettings {
	out := &panmailv1.SentCopySettings{
		Mode:              modeToProto[s.Mode],
		ImapHost:          s.IMAPHost,
		ImapPort:          int32(s.IMAPPort),
		ImapUseSsl:        s.IMAPUseSSL,
		SentFolder:        s.SentFolder,
		MasterUsername:    s.MasterUsername,
		MasterPasswordSet: s.MasterPassword != "",
		MasterSeparator:   s.MasterSeparator,
	}
	if out.Mode == panmailv1.SentCopyMode_SENT_COPY_MODE_UNSPECIFIED {
		out.Mode = panmailv1.SentCopyMode_SENT_COPY_MODE_NONE
	}
	if !s.UpdatedAt.IsZero() {
		out.UpdatedAt = s.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func mailboxToProto(m *entities.Mailbox) *panmailv1.SentCopyMailbox {
	out := &panmailv1.SentCopyMailbox{
		Address:     m.Address,
		Username:    m.Username,
		PasswordSet: m.Password != "",
	}
	if !m.UpdatedAt.IsZero() {
		out.UpdatedAt = m.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func userID(ctx context.Context) string {
	id, _ := authmiddlewares.GetUserID(ctx)
	return id
}
