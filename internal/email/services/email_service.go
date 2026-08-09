package services

import (
	"context"
	"errors"
	"math"
	"strconv"

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
		// Over the send rate is a distinct outcome from a failed send, and a
		// client can only act on it if it is told so. ResourceExhausted is the
		// code a Connect client surfaces as 429, and Retry-After turns "try
		// again" into something a caller can schedule against instead of
		// guessing — clients that guess tend to retry immediately and make the
		// overload worse.
		var limited *usecases.RateLimitedError
		if errors.As(err, &limited) {
			connectErr := connect.NewError(connect.CodeResourceExhausted, limited)
			seconds := int(math.Ceil(limited.RetryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			connectErr.Meta().Set("Retry-After", strconv.Itoa(seconds))
			return nil, connectErr
		}
		return nil, err
	}
	return connect.NewResponse(res), nil
}
