package services

import (
	"context"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/api/panmail/v1/panmailv1connect"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	"github.com/gsoultan/panmail/internal/system_settings/usecases"
)

type settingsService struct {
	usecase        usecases.SettingsUsecase
	smtpSubmission *panmailv1.SmtpSubmission
}

// NewSettingsService builds the settings handler.
//
// smtpSubmission describes the SMTP listener this process is running, and is
// reported unchanged on every read. It is a value rather than something the
// service looks up because it cannot change while the process is alive: it
// comes from the flags the process started with.
func NewSettingsService(
	usecase usecases.SettingsUsecase,
	smtpSubmission *panmailv1.SmtpSubmission,
) panmailv1connect.SystemSettingsServiceHandler {
	return &settingsService{usecase: usecase, smtpSubmission: smtpSubmission}
}

func (s *settingsService) GetSettings(ctx context.Context, req *connect.Request[panmailv1.GetSettingsRequest]) (*connect.Response[panmailv1.GetSettingsResponse], error) {
	settings, err := s.usecase.GetSettings(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&panmailv1.GetSettingsResponse{
		Settings:       settings,
		SmtpSubmission: s.smtpSubmission,
	}), nil
}

func (s *settingsService) UpdateSettings(ctx context.Context, req *connect.Request[panmailv1.UpdateSettingsRequest]) (*connect.Response[panmailv1.UpdateSettingsResponse], error) {
	// Read the current values first, so the log can say what actually changed
	// rather than only what was submitted.
	before, err := s.usecase.GetSettings(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	settings, err := s.usecase.UpdateSettings(ctx, req.Msg.Settings)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logRetentionChange(ctx, before, settings)
	return connect.NewResponse(&panmailv1.UpdateSettingsResponse{Settings: settings}), nil
}

// logRetentionChange records who changed a retention policy, and to what.
//
// This is the most destructive call the API exposes. Saving a retention starts
// a pass immediately, so setting message content to one day removes every body
// older than that within seconds -- and until now nothing recorded that anyone
// had asked. The worker logs the effect ("retention pruned, removed 48000"),
// which after an incident tells you a month of message bodies is gone and
// nothing whatsoever about who decided it should be.
//
// Admin-only is not an answer to that. Admin accounts get shared, phished, and
// used by tired people at the end of a long day, and all three cases look
// identical in a log that names no actor.
func logRetentionChange(ctx context.Context, before, after *panmailv1.SystemSettings) {
	changes := retentionChanges(before, after)
	if len(changes) == 0 {
		return
	}

	userID, _ := middlewares.GetUserID(ctx)
	if userID == "" {
		// An API key rather than a session. The key's own id is not on the
		// principal, so the tenant and role are what can honestly be said.
		userID = "(api key)"
	}

	slog.Warn("retention policy changed",
		"actor", userID,
		"role", middlewares.GetRole(ctx),
		"tenant_id", middlewares.GetTenantID(ctx),
		"changes", changes)
}

// retentionChanges describes every retention field that moved, in the terms an
// operator reading an incident timeline needs: which class, from what to what,
// and whether the change destroys data.
func retentionChanges(before, after *panmailv1.SystemSettings) []string {
	if before == nil || after == nil {
		return nil
	}

	fields := []struct {
		name        string
		from, to    int32
		destructive bool
	}{
		{"events", before.LogRetentionDays, after.LogRetentionDays, false},
		{"message_content", before.MessageRetentionDays, after.MessageRetentionDays, true},
		{"archives", before.ArchiveRetentionDays, after.ArchiveRetentionDays, true},
		{"inbound_mail", before.InboundRetentionDays, after.InboundRetentionDays, true},
		{"outbox", before.OutboxRetentionDays, after.OutboxRetentionDays, false},
		{"webhooks", before.WebhookRetentionDays, after.WebhookRetentionDays, false},
		{"app_logs", before.AppLogRetentionDays, after.AppLogRetentionDays, false},
		// Destructive: a held message that expires was never decided by
		// anyone, and shortening this is how one disappears.
		{"quarantine", before.QuarantineRetentionDays, after.QuarantineRetentionDays, true},
	}

	var changes []string
	for _, f := range fields {
		if f.from == f.to {
			continue
		}

		// A policy only deletes when it gets shorter, and zero is not shorter:
		// it means keep forever. Switching one on, or lowering it, is what
		// removes data that existed a moment ago.
		shortens := (f.from == 0 && f.to > 0) || (f.from > 0 && f.to > 0 && f.to < f.from)

		note := ""
		if shortens && f.destructive {
			note = " DELETES DATA NOW"
		}
		changes = append(changes, fmt.Sprintf("%s %s->%s%s",
			f.name, days(f.from), days(f.to), note))
	}
	return changes
}

func days(d int32) string {
	if d <= 0 {
		return "forever"
	}
	return fmt.Sprintf("%dd", d)
}
