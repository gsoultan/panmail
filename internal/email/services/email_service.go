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

// IdempotencyKeyHeader carries a send's idempotency key for a client that
// would rather not put it in the body. The field is the canonical place; the
// header exists because it is where HTTP APIs conventionally take one, and a
// generic retrying HTTP client can add it without understanding the message.
const IdempotencyKeyHeader = "Idempotency-Key"

func (s *emailService) SendEmail(ctx context.Context, req *connect.Request[panmailv1.SendEmailRequest]) (*connect.Response[panmailv1.SendEmailResponse], error) {
	tenantID := middlewares.GetTenantID(ctx)

	// The header fills an empty field, and must agree with a set one. Two
	// different keys on one request have no reading that is safe to guess at:
	// picking either could replay a message the caller did not mean.
	if header := req.Header().Get(IdempotencyKeyHeader); header != "" {
		switch req.Msg.IdempotencyKey {
		case "":
			req.Msg.IdempotencyKey = header
		case header:
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("idempotency_key and the Idempotency-Key header disagree"))
		}
	}

	res, err := s.sendEmailUsecase.SendEmail(ctx, tenantID, req.Msg)
	if err != nil {
		// Over the send rate is a distinct outcome from a failed send, and a
		// client can only act on it if it is told so. ResourceExhausted is the
		// code a Connect client surfaces as 429, and Retry-After turns "try
		// again" into something a caller can schedule against instead of
		// guessing — clients that guess tend to retry immediately and make the
		// overload worse.
		// A backlog beyond what the tenant's rate can drain. Same code as the
		// rate refusal because it is the same answer to the client — you are
		// asking for more than you may have — but without a Retry-After, since
		// the queue clearing is not something a caller can schedule against.
		var full *usecases.BacklogFullError
		if errors.As(err, &full) {
			return nil, connect.NewError(connect.CodeResourceExhausted, full)
		}

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
		// The refusals below are decisions, not failures: the same request
		// will be refused identically until somebody changes something. A
		// bare error would reach the client as CodeUnknown and HTTP 500,
		// which reads as "try again later" and is the one thing that cannot
		// help.
		var suppressed *usecases.SuppressedRecipientError
		if errors.As(err, &suppressed) {
			// FailedPrecondition rather than InvalidArgument: the request is
			// well formed and the caller is allowed to make it, but the
			// system is in a state where it cannot succeed until the
			// suppression is lifted.
			return nil, connect.NewError(connect.CodeFailedPrecondition, suppressed)
		}

		// Same reasoning as suppression: the request is well formed and the
		// caller may make it, but the provider's allowed senders refuse it and
		// will keep refusing until an operator changes them.
		var wrongSender *usecases.ProviderSenderRefusedError
		if errors.As(err, &wrongSender) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, wrongSender)
		}

		var noProvider *usecases.ProviderNotFoundError
		if errors.As(err, &noProvider) {
			return nil, connect.NewError(connect.CodeInvalidArgument, noProvider)
		}

		var badHeader *usecases.InvalidReplyHeaderError
		if errors.As(err, &badHeader) {
			return nil, connect.NewError(connect.CodeInvalidArgument, badHeader)
		}

		var badKey *usecases.InvalidIdempotencyKeyError
		if errors.As(err, &badKey) {
			return nil, connect.NewError(connect.CodeInvalidArgument, badKey)
		}

		// AlreadyExists: the key names a send that exists and is not this
		// one. Not retryable as it stands -- the caller has to use a new key,
		// or send what it sent the first time.
		var reused *usecases.IdempotencyKeyConflictError
		if errors.As(err, &reused) {
			return nil, connect.NewError(connect.CodeAlreadyExists, reused)
		}

		// Aborted, which clients treat as retryable: a concurrent request with
		// the same key won and its outcome could not be read yet. Asking again
		// finds it.
		var inFlight *usecases.IdempotencyKeyInFlightError
		if errors.As(err, &inFlight) {
			return nil, connect.NewError(connect.CodeAborted, inFlight)
		}

		var badTemplate *usecases.TemplateRefusedError
		if errors.As(err, &badTemplate) {
			return nil, connect.NewError(connect.CodeInvalidArgument, badTemplate)
		}

		return nil, err
	}
	return connect.NewResponse(res), nil
}
