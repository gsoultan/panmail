package services

import (
	"errors"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/emailfilter"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The wire uses enums and the domain uses strings. The mapping is explicit in
// both directions rather than a cast, so an enum value added to the proto
// without being handled here fails to compile instead of arriving as an empty
// string that silently matches nothing.

var directionsToProto = map[emailfilter.Direction]panmailv1.FilterDirection{
	emailfilter.DirectionOutbound: panmailv1.FilterDirection_FILTER_DIRECTION_OUTBOUND,
	emailfilter.DirectionInbound:  panmailv1.FilterDirection_FILTER_DIRECTION_INBOUND,
}

var directionsFromProto = map[panmailv1.FilterDirection]emailfilter.Direction{
	panmailv1.FilterDirection_FILTER_DIRECTION_OUTBOUND: emailfilter.DirectionOutbound,
	panmailv1.FilterDirection_FILTER_DIRECTION_INBOUND:  emailfilter.DirectionInbound,
}

var actionsToProto = map[emailfilter.Action]panmailv1.FilterAction{
	emailfilter.ActionHold:   panmailv1.FilterAction_FILTER_ACTION_HOLD,
	emailfilter.ActionReject: panmailv1.FilterAction_FILTER_ACTION_REJECT,
	emailfilter.ActionTag:    panmailv1.FilterAction_FILTER_ACTION_TAG,
	emailfilter.ActionAllow:  panmailv1.FilterAction_FILTER_ACTION_ALLOW,
}

var actionsFromProto = map[panmailv1.FilterAction]emailfilter.Action{
	panmailv1.FilterAction_FILTER_ACTION_HOLD:   emailfilter.ActionHold,
	panmailv1.FilterAction_FILTER_ACTION_REJECT: emailfilter.ActionReject,
	panmailv1.FilterAction_FILTER_ACTION_TAG:    emailfilter.ActionTag,
	panmailv1.FilterAction_FILTER_ACTION_ALLOW:  emailfilter.ActionAllow,
}

var statusesToProto = map[emailfilter.Status]panmailv1.FilterStatus{
	emailfilter.StatusPending:  panmailv1.FilterStatus_FILTER_STATUS_PENDING,
	emailfilter.StatusReleased: panmailv1.FilterStatus_FILTER_STATUS_RELEASED,
	emailfilter.StatusRejected: panmailv1.FilterStatus_FILTER_STATUS_REJECTED,
	emailfilter.StatusExpired:  panmailv1.FilterStatus_FILTER_STATUS_EXPIRED,
}

var statusesFromProto = map[panmailv1.FilterStatus]emailfilter.Status{
	panmailv1.FilterStatus_FILTER_STATUS_PENDING:  emailfilter.StatusPending,
	panmailv1.FilterStatus_FILTER_STATUS_RELEASED: emailfilter.StatusReleased,
	panmailv1.FilterStatus_FILTER_STATUS_REJECTED: emailfilter.StatusRejected,
	panmailv1.FilterStatus_FILTER_STATUS_EXPIRED:  emailfilter.StatusExpired,
}

// directionFromProto returns the empty direction for UNSPECIFIED, which the
// listing treats as "either" rather than as an error.
func directionFromProto(d panmailv1.FilterDirection) emailfilter.Direction {
	return directionsFromProto[d]
}

func statusFromProto(s panmailv1.FilterStatus) emailfilter.Status {
	return statusesFromProto[s]
}

func conditionsToProto(in []emailfilter.Condition) []*panmailv1.FilterCondition {
	if len(in) == 0 {
		return nil
	}
	out := make([]*panmailv1.FilterCondition, 0, len(in))
	for _, c := range in {
		out = append(out, &panmailv1.FilterCondition{
			Field:    string(c.Field),
			Operator: string(c.Operator),
			Values:   c.Values,
			Header:   c.Header,
			Number:   c.Number,
		})
	}
	return out
}

func conditionsFromProto(in []*panmailv1.FilterCondition) []emailfilter.Condition {
	if len(in) == 0 {
		return nil
	}
	out := make([]emailfilter.Condition, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		out = append(out, emailfilter.Condition{
			Field:    emailfilter.Field(c.GetField()),
			Operator: emailfilter.Operator(c.GetOperator()),
			Values:   c.GetValues(),
			Header:   c.GetHeader(),
			Number:   c.GetNumber(),
		})
	}
	return out
}

func ruleToProto(r *emailfilter.Rule) *panmailv1.FilterRule {
	return &panmailv1.FilterRule{
		Id:         r.ID,
		Name:       r.Name,
		Direction:  directionsToProto[r.Direction],
		Action:     actionsToProto[r.Action],
		Priority:   int32(r.Priority),
		Enabled:    r.Enabled,
		Conditions: conditionsToProto(r.Conditions),
		Exceptions: conditionsToProto(r.Exceptions),
		Tag:        r.Tag,
	}
}

func ruleFromProto(in *panmailv1.FilterRule) (*emailfilter.Rule, error) {
	if in == nil {
		return nil, errors.New("a rule is required")
	}
	direction, ok := directionsFromProto[in.GetDirection()]
	if !ok {
		return nil, errors.New("a direction is required")
	}
	action, ok := actionsFromProto[in.GetAction()]
	if !ok {
		return nil, errors.New("an action is required")
	}
	return &emailfilter.Rule{
		ID:         in.GetId(),
		Name:       in.GetName(),
		Direction:  direction,
		Action:     action,
		Priority:   int(in.GetPriority()),
		Enabled:    in.GetEnabled(),
		Conditions: conditionsFromProto(in.GetConditions()),
		Exceptions: conditionsFromProto(in.GetExceptions()),
		Tag:        in.GetTag(),
	}, nil
}

func filteredToProto(m *emailfilter.FilteredMessage) *panmailv1.FilteredMessage {
	if m == nil {
		return nil
	}
	out := &panmailv1.FilteredMessage{
		Id:              m.ID,
		Direction:       directionsToProto[m.Direction],
		RuleId:          m.RuleID,
		RuleName:        m.RuleName,
		Action:          actionsToProto[m.Action],
		Status:          statusesToProto[m.Status],
		MessageId:       m.MessageID,
		ProviderId:      m.ProviderID,
		From:            m.From,
		Recipients:      m.Recipients,
		Subject:         m.Subject,
		SizeBytes:       m.SizeBytes,
		AttachmentCount: int32(m.AttachmentCount),
		AttachmentNames: m.AttachmentNames,
		Matched:         conditionsToProto(m.Matched),
		ReviewedBy:      m.ReviewedBy,
		ReviewNote:      m.ReviewNote,
		CreatedAt:       timestamppb.New(m.CreatedAt),
	}
	if m.ReviewedAt != nil {
		out.ReviewedAt = timestamppb.New(*m.ReviewedAt)
	}
	if m.ExpiresAt != nil {
		out.ExpiresAt = timestamppb.New(*m.ExpiresAt)
	}
	return out
}
