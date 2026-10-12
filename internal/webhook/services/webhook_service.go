package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	authmiddlewares "github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/webhook/egress"
	"github.com/gsoultan/panmail/internal/webhook/samples"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
	"github.com/gsoultan/panmail/internal/webhook/worker"
)

type webhookService struct {
	usecase usecases.WebhookUsecase
	tester  *worker.Tester
}

// Option configures the webhook service.
type Option func(*webhookService)

// WithTester sets what TestWebhook sends through. Pass the one built with the
// operator's egress restriction, so a test cannot reach what a real delivery
// may not. Without it TestWebhook uses an unrestricted tester.
func WithTester(t *worker.Tester) Option {
	return func(s *webhookService) { s.tester = t }
}

func NewWebhookService(usecase usecases.WebhookUsecase, opts ...Option) panmailv1connect.WebhookServiceHandler {
	s := &webhookService{usecase: usecase}
	for _, opt := range opts {
		opt(s)
	}
	if s.tester == nil {
		s.tester = worker.NewTester()
	}
	return s
}

func (s *webhookService) CreateWebhook(ctx context.Context, req *connect.Request[panmailv1.CreateWebhookRequest]) (*connect.Response[panmailv1.CreateWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhook, err := s.usecase.Create(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, saveError(err)
	}
	return connect.NewResponse(&panmailv1.CreateWebhookResponse{Webhook: webhook}), nil
}

func (s *webhookService) ListWebhooks(ctx context.Context, req *connect.Request[panmailv1.ListWebhooksRequest]) (*connect.Response[panmailv1.ListWebhooksResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhooks, nextPageToken, err := s.usecase.List(ctx, tenantID, int(req.Msg.PageSize), req.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.ListWebhooksResponse{
		Webhooks:      webhooks,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *webhookService) DeleteWebhook(ctx context.Context, req *connect.Request[panmailv1.DeleteWebhookRequest]) (*connect.Response[panmailv1.DeleteWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	if err := s.usecase.Delete(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.DeleteWebhookResponse{}), nil
}

func (s *webhookService) UpdateWebhook(ctx context.Context, req *connect.Request[panmailv1.UpdateWebhookRequest]) (*connect.Response[panmailv1.UpdateWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	webhook, err := s.usecase.Update(ctx, tenantID, req.Msg)
	if err != nil {
		return nil, saveError(err)
	}
	return connect.NewResponse(&panmailv1.UpdateWebhookResponse{Webhook: webhook}), nil
}

func (s *webhookService) TestWebhook(ctx context.Context, req *connect.Request[panmailv1.TestWebhookRequest]) (*connect.Response[panmailv1.TestWebhookResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a webhook id is required"))
	}
	sub, err := s.usecase.GetSubscription(ctx, tenantID, req.Msg.Id)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && sub == nil):
		return nil, connect.NewError(connect.CodeNotFound, errors.New("webhook not found"))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	event := req.Msg.Event
	if event == panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_UNSPECIFIED {
		// The first event the subscription listens for, so the test looks
		// like something it will actually receive.
		event = panmailv1.WebhookTriggerEvent_WEBHOOK_TRIGGER_EVENT_MAIL_SENT
		if len(sub.Events) > 0 {
			event = panmailv1.WebhookTriggerEvent(sub.Events[0])
		}
	}
	if !samples.Known(event) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown webhook event %d", event))
	}

	body, err := worker.Envelope(tenantID, event, samples.Data(event, tenantID, time.Now()), time.Now())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	result := s.tester.Send(ctx, sub, event.String(), body)
	res := &panmailv1.TestWebhookResponse{
		StatusCode:   int32(result.StatusCode),
		ResponseBody: result.Body,
		DurationMs:   result.Duration.Milliseconds(),
		Event:        event,
		Payload:      string(body),
	}
	switch {
	case result.Err != nil:
		res.Error = result.Err.Error()
	case result.StatusCode >= 200 && result.StatusCode < 300:
		res.Success = true
	default:
		res.Error = fmt.Sprintf("endpoint returned %d", result.StatusCode)
	}
	// An endpoint that refused or could not be reached is the answer the test
	// was asked for, not a failure of the call, so it comes back as a result.
	return connect.NewResponse(res), nil
}

func (s *webhookService) ListWebhookEventSamples(ctx context.Context, _ *connect.Request[panmailv1.ListWebhookEventSamplesRequest]) (*connect.Response[panmailv1.ListWebhookEventSamplesResponse], error) {
	tenantID := authmiddlewares.GetTenantID(ctx)
	now := time.Now()
	events := samples.Events()
	out := make([]*panmailv1.WebhookEventSample, 0, len(events))
	for _, event := range events {
		body, err := worker.Envelope(tenantID, event, samples.Data(event, tenantID, now), now)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		// Re-indented rather than marshalled with indentation, so the bytes
		// shown are the bytes sent, only laid out to be read.
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		out = append(out, &panmailv1.WebhookEventSample{
			Event:       event,
			Description: samples.Description(event),
			Payload:     pretty.String(),
		})
	}
	return connect.NewResponse(&panmailv1.ListWebhookEventSamplesResponse{Samples: out}), nil
}

// saveError maps a failed create or update to a status. A URL the operator's
// egress policy refuses is the caller's to fix, and says so; anything else is
// ours.
func saveError(err error) error {
	if errors.Is(err, egress.ErrDisallowed) {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%w; this gateway only delivers webhooks to public addresses", err))
	}
	return connect.NewError(connect.CodeInternal, err)
}
