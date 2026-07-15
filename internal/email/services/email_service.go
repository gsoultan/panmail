package services

import (
	"context"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/email/usecases"
)

type emailService struct {
	sendEmailUsecase usecases.SendEmailUsecase
}

func NewEmailService(sendEmailUsecase usecases.SendEmailUsecase) panmailv1connect.EmailServiceHandler {
	return &emailService{
		sendEmailUsecase: sendEmailUsecase,
	}
}

func (s *emailService) SendEmail(ctx context.Context, req *connect.Request[panmailv1.SendEmailRequest]) (*connect.Response[panmailv1.SendEmailResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)
	res, err := s.sendEmailUsecase.SendEmail(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(res), nil
}
