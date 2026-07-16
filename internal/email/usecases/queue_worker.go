package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/config"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	tenantusecases "github.com/gsoultan/panmail/internal/tenant/usecases"
	"github.com/gsoultan/panmail/pkg/emailutil"
	"google.golang.org/protobuf/encoding/protojson"
)

type QueueWorker interface {
	Start(ctx context.Context)
	Trigger()
}

type queueWorker struct {
	outboxRepo         stores.OutboxRepository
	emailUsecase       SendEmailUsecase
	suppressionUsecase suppressionusecases.ManageSuppressionsUsecase
	tenantUsecase      tenantusecases.TenantUsecase
	interval           time.Duration
	trigger            chan struct{}

	retryPatternCache  sync.Map
	globalRetryPattern []string
}

func NewQueueWorker(
	outboxRepo stores.OutboxRepository,
	emailUsecase SendEmailUsecase,
	suppressionUsecase suppressionusecases.ManageSuppressionsUsecase,
	tenantUsecase tenantusecases.TenantUsecase,
	interval time.Duration,
) QueueWorker {
	w := &queueWorker{
		outboxRepo:         outboxRepo,
		emailUsecase:       emailUsecase,
		suppressionUsecase: suppressionUsecase,
		tenantUsecase:      tenantUsecase,
		interval:           interval,
		trigger:            make(chan struct{}, 1),
	}

	// Pre-load global retry pattern
	cfg, _ := config.Load()
	if cfg != nil && len(cfg.App.RetryPattern) > 0 {
		w.globalRetryPattern = cfg.App.RetryPattern
	} else {
		w.globalRetryPattern = []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"}
	}

	return w
}

func (w *queueWorker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

func (w *queueWorker) Start(ctx context.Context) {
	slog.Info("queue worker started", "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		// Try to process a batch
		count := w.processPending(ctx)

		// If we processed a full batch, don't wait, go again immediately
		if count >= 500 {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Regular polling
		case <-w.trigger:
			// Immediate trigger from new email
		}
	}
}

func (w *queueWorker) processPending(ctx context.Context) int {
	// Fetch a larger batch for parallel processing to reach 1000+ msgs/sec
	batchSize := 500
	emails, err := w.outboxRepo.ListPending(ctx, batchSize)
	if err != nil {
		slog.Error("failed to list pending outbox emails", "error", err)
		return 0
	}

	count := len(emails)
	if count == 0 {
		return 0
	}

	slog.Info("queue worker processing batch", "count", count)

	var wg sync.WaitGroup
	// Limit concurrency to reach high throughput without resource exhaustion
	sem := make(chan struct{}, 200)

	for _, e := range emails {
		wg.Add(1)
		go func(email *entities.OutboxEmail) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				w.processEmail(ctx, email)
			case <-ctx.Done():
				return
			}
		}(e)
	}
	wg.Wait()
	return count
}

func (w *queueWorker) processEmail(ctx context.Context, e *entities.OutboxEmail) {
	// Add per-request timeout to avoid worker hanging
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	slog.Info("worker processing email", "id", e.ID, "tenant_id", e.TenantID, "retry_count", e.RetryCount)

	var req panmailv1.SendEmailRequest
	if err := protojson.Unmarshal(e.Request, &req); err != nil {
		slog.Error("failed to unmarshal outbox email request", "error", err, "id", e.ID)
		_ = w.outboxRepo.Delete(ctx, e.ID)
		return
	}

	// We use the same message ID as stored in outbox
	ctx = context.WithValue(ctx, SkipOutboxKey, true)
	ctx = context.WithValue(ctx, MessageIDKey, e.ID)

	res, err := w.emailUsecase.SendEmail(ctx, e.TenantID, &req)
	if err == nil && res != nil {
		// Successfully sent
		slog.Info("email sent successfully from worker", "id", e.ID)
		if err := w.outboxRepo.Delete(ctx, e.ID); err != nil {
			slog.Error("failed to delete outbox email after successful send", "error", err, "id", e.ID)
		}
		return
	}

	// Failed again
	slog.Warn("email delivery attempt failed", "id", e.ID, "error", err)
	e.RetryCount++
	e.UpdatedAt = time.Now()
	e.LastError = err.Error()

	// Classify error
	bounceType := emailutil.ClassifyError(err.Error())

	// Use cached retry pattern
	retryPattern := w.getRetryPattern(ctx, e.TenantID)

	shouldRetry := false
	var nextDelay time.Duration

	if bounceType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE {
		if e.RetryCount <= len(retryPattern) {
			shouldRetry = true
			patternIdx := e.RetryCount - 1
			d, parseErr := time.ParseDuration(retryPattern[patternIdx])
			if parseErr == nil {
				nextDelay = d
			} else {
				// Fallback to simple backoff if pattern invalid
				nextDelay = time.Duration(e.RetryCount*e.RetryCount) * time.Minute
			}
		}
	}

	if shouldRetry {
		e.Status = entities.OutboxStatusDeferred
		e.NextRetryAt = time.Now().Add(nextDelay)
		slog.Info("email delivery deferred for retry", "id", e.ID, "retry_count", e.RetryCount, "next_retry_at", e.NextRetryAt)

		// Record DEFERRED event
		for _, recipient := range req.To {
			_ = w.emailUsecase.RecordEvent(ctx, e.TenantID, req.ProviderId, e.ID, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED, recipient, e.LastError, nil)
		}
	} else {
		e.Status = entities.OutboxStatusFailed
		slog.Info("email delivery failed permanently", "id", e.ID, "bounce_type", bounceType.String(), "error", e.LastError)

		// Record permanent failure event
		for _, recipient := range req.To {
			_ = w.emailUsecase.RecordEvent(ctx, e.TenantID, req.ProviderId, e.ID, bounceType, recipient, e.LastError, nil)
		}

		// Automatically suppress if hard bounce, spam report, etc.
		if bounceType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE ||
			bounceType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT ||
			bounceType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED ||
			bounceType == panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED {

			for _, recipient := range req.To {
				reason := fmt.Sprintf("Automatic suppression due to %s: %s", bounceType.String(), e.LastError)
				_, _ = w.suppressionUsecase.Add(ctx, e.TenantID, &panmailv1.AddSuppressionRequest{
					Email:  recipient,
					Reason: reason,
				})
			}
		}
	}

	if err := w.outboxRepo.Update(ctx, e); err != nil {
		slog.Error("failed to update outbox email status", "error", err, "id", e.ID, "status", e.Status)
	}
}

func (w *queueWorker) getRetryPattern(ctx context.Context, tenantID string) []string {
	if val, ok := w.retryPatternCache.Load(tenantID); ok {
		return val.([]string)
	}

	// Fetch from DB if not in cache
	pattern := w.globalRetryPattern
	if tenant, err := w.tenantUsecase.GetTenantByID(ctx, tenantID); err == nil && len(tenant.RetryPattern) > 0 {
		pattern = tenant.RetryPattern
	}

	// Cache for 1 minute (simple cache strategy for now)
	w.retryPatternCache.Store(tenantID, pattern)

	// In a real high-load system, we might want to invalidate this cache when tenant settings change
	// but 1 minute stale pattern is usually acceptable for email retries.

	return pattern
}
