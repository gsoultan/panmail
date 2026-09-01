// Package services exposes filtering over ConnectRPC.
package services

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/emailfilter"
)

type service struct {
	panmailv1connect.UnimplementedEmailFilterServiceHandler

	rules      emailfilter.RuleRepository
	quarantine emailfilter.QuarantineRepository
	reviewer   emailfilter.Reviewer
}

// NewService wires the handlers to their stores.
func NewService(
	rules emailfilter.RuleRepository,
	quarantine emailfilter.QuarantineRepository,
	reviewer emailfilter.Reviewer,
) panmailv1connect.EmailFilterServiceHandler {
	return &service{rules: rules, quarantine: quarantine, reviewer: reviewer}
}

// tenantOf reads the tenant the auth middleware resolved from the key. It is
// never taken from the request: a caller that could name its own tenant could
// read and release another tenant's mail.
func tenantOf(ctx context.Context) (string, error) {
	tenantID := middlewares.GetTenantID(ctx)
	if tenantID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("no tenant on the request"))
	}
	return tenantID, nil
}

// reviewerOf names who decided. An API key has no user behind it, and
// recording an empty string would read as "nobody", so a key-authenticated
// release is attributed to the key rather than left blank.
func reviewerOf(ctx context.Context) string {
	if userID, ok := middlewares.GetUserID(ctx); ok && userID != "" {
		return userID
	}
	return "api-key"
}

// asConnectError maps the domain's errors onto codes a client can switch on.
func asConnectError(err error) error {
	switch {
	case errors.Is(err, emailfilter.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, emailfilter.ErrAlreadyReviewed):
		// Not an internal error and not a bad request: the caller asked for
		// something reasonable and lost a race. FailedPrecondition says so,
		// and a UI can turn it into "somebody else already handled this".
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (s *service) ListFilterRules(
	ctx context.Context, _ *connect.Request[panmailv1.ListFilterRulesRequest],
) (*connect.Response[panmailv1.ListFilterRulesResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := s.rules.List(ctx, tenantID)
	if err != nil {
		return nil, asConnectError(err)
	}
	out := make([]*panmailv1.FilterRule, 0, len(rules))
	for i := range rules {
		out = append(out, ruleToProto(&rules[i]))
	}
	return connect.NewResponse(&panmailv1.ListFilterRulesResponse{Rules: out}), nil
}

func (s *service) CreateFilterRule(
	ctx context.Context, req *connect.Request[panmailv1.CreateFilterRuleRequest],
) (*connect.Response[panmailv1.CreateFilterRuleResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	rule, err := ruleFromProto(req.Msg.GetRule())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rule.TenantID = tenantID
	rule.ID = uuid.New().String()

	// Validated here so a bad pattern is an invalid_argument the author can
	// fix, rather than an internal error surfacing from the store.
	if err := rule.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.rules.Create(ctx, rule); err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.CreateFilterRuleResponse{Rule: ruleToProto(rule)}), nil
}

func (s *service) UpdateFilterRule(
	ctx context.Context, req *connect.Request[panmailv1.UpdateFilterRuleRequest],
) (*connect.Response[panmailv1.UpdateFilterRuleResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	rule, err := ruleFromProto(req.Msg.GetRule())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if rule.ID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a rule id is required"))
	}
	rule.TenantID = tenantID
	if err := rule.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.rules.Update(ctx, rule); err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.UpdateFilterRuleResponse{Rule: ruleToProto(rule)}), nil
}

func (s *service) DeleteFilterRule(
	ctx context.Context, req *connect.Request[panmailv1.DeleteFilterRuleRequest],
) (*connect.Response[panmailv1.DeleteFilterRuleResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.rules.Delete(ctx, tenantID, req.Msg.GetId()); err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.DeleteFilterRuleResponse{}), nil
}

func (s *service) ListFilteredMessages(
	ctx context.Context, req *connect.Request[panmailv1.ListFilteredMessagesRequest],
) (*connect.Response[panmailv1.ListFilteredMessagesResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	messages, next, err := s.quarantine.List(ctx, tenantID, emailfilter.QuarantineFilter{
		Direction: directionFromProto(req.Msg.GetDirection()),
		Status:    statusFromProto(req.Msg.GetStatus()),
		PageSize:  int(req.Msg.GetPageSize()),
		PageToken: req.Msg.GetPageToken(),
	})
	if err != nil {
		return nil, asConnectError(err)
	}
	out := make([]*panmailv1.FilteredMessage, 0, len(messages))
	for i := range messages {
		out = append(out, filteredToProto(&messages[i]))
	}
	return connect.NewResponse(&panmailv1.ListFilteredMessagesResponse{
		Messages: out, NextPageToken: next,
	}), nil
}

func (s *service) GetFilteredMessage(
	ctx context.Context, req *connect.Request[panmailv1.GetFilteredMessageRequest],
) (*connect.Response[panmailv1.GetFilteredMessageResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	message, err := s.quarantine.Get(ctx, tenantID, req.Msg.GetId())
	if err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.GetFilteredMessageResponse{
		Message: filteredToProto(message),
	}), nil
}

func (s *service) ReleaseFilteredMessage(
	ctx context.Context, req *connect.Request[panmailv1.ReleaseFilteredMessageRequest],
) (*connect.Response[panmailv1.ReleaseFilteredMessageResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	// Who decided is recorded, because releasing a held message is the one
	// action here that puts mail on the wire.
	message, err := s.reviewer.Release(ctx, tenantID, req.Msg.GetId(), reviewerOf(ctx), req.Msg.GetNote())
	if err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.ReleaseFilteredMessageResponse{
		Message: filteredToProto(message),
	}), nil
}

func (s *service) RejectFilteredMessage(
	ctx context.Context, req *connect.Request[panmailv1.RejectFilteredMessageRequest],
) (*connect.Response[panmailv1.RejectFilteredMessageResponse], error) {
	tenantID, err := tenantOf(ctx)
	if err != nil {
		return nil, err
	}
	message, err := s.reviewer.Reject(ctx, tenantID, req.Msg.GetId(), reviewerOf(ctx), req.Msg.GetNote())
	if err != nil {
		return nil, asConnectError(err)
	}
	return connect.NewResponse(&panmailv1.RejectFilteredMessageResponse{
		Message: filteredToProto(message),
	}), nil
}
