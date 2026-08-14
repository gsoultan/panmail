package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/webhook/repositories/entities"
	"github.com/gsoultan/panmail/internal/webhook/repositories/stores"
	"github.com/gsoultan/panmail/internal/webhook/usecases"
)

// DurableWorker delivers webhook notifications from a persisted queue.
//
// It replaces an in-memory channel that lost notifications three ways: a
// failing endpoint had its notification logged and dropped, a full channel
// dropped with a warning, and a restart lost everything queued. For the
// mechanism whose entire job is telling a tenant what happened to their mail,
// that is silent and unrecoverable — a tenant whose endpoint blipped for
// thirty seconds simply never learned that mail bounced.
//
// One row per subscription, so a broken endpoint retries on its own schedule
// without holding up a working one beside it.
type DurableWorker struct {
	deliveries     stores.DeliveryRepository
	webhookUsecase usecases.WebhookUsecase
	client         *http.Client

	interval  time.Duration
	batchSize int
	lease     time.Duration
	retention time.Duration

	// trigger wakes the loop when something is enqueued, so a notification
	// does not wait out the poll interval before its first attempt.
	trigger   chan struct{}
	lastPrune time.Time
}

const (
	defaultDeliveryInterval  = 5 * time.Second
	defaultDeliveryBatch     = 50
	defaultDeliveryLease     = 2 * time.Minute
	defaultDeliveryTimeout   = 15 * time.Second
	defaultDeliveryRetention = 7 * 24 * time.Hour
	deliveryPrunePeriod      = time.Hour

	// A receiver that sends a novel back gets read this far and no further;
	// the body is only kept to make the failure diagnosable.
	maxErrorBodyBytes = 2048

	// How long a delivery already in flight gets to finish once shutdown
	// begins. Comfortably above defaultDeliveryTimeout, so a request that was
	// going to complete does, and well inside a default termination grace
	// period.
	drainGrace = 20 * time.Second
)

func NewDurableWorker(deliveries stores.DeliveryRepository, webhookUsecase usecases.WebhookUsecase) *DurableWorker {
	return &DurableWorker{
		deliveries:     deliveries,
		webhookUsecase: webhookUsecase,
		client:         &http.Client{Timeout: defaultDeliveryTimeout},
		interval:       defaultDeliveryInterval,
		batchSize:      defaultDeliveryBatch,
		lease:          defaultDeliveryLease,
		retention:      defaultDeliveryRetention,
		trigger:        make(chan struct{}, 1),
	}
}

// Enqueue records a notification for every subscription that wants this event.
//
// It writes rather than buffers, which is the whole point: once this returns
// the notification survives a crash. The signature keeps no error because its
// callers are event-recording paths that must not fail because a webhook could
// not be written — a failure here is logged and the event still counts.
func (w *DurableWorker) Enqueue(tenantID string, event panmailv1.WebhookTriggerEvent, payload any) {
	// Detached from the caller's context on purpose. The request that produced
	// the event may already be finishing, and losing the notification because
	// its originating context was cancelled is the failure mode being removed.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()

	subscriptions, err := w.webhookUsecase.ListActiveByEvent(ctx, tenantID, event)
	if err != nil {
		slog.Error("webhook: failed to list subscriptions", "tenant_id", tenantID, "event", event, "error", err)
		return
	}
	if len(subscriptions) == 0 {
		return
	}

	body, err := json.Marshal(map[string]any{
		"event":     event.String(),
		"tenant_id": tenantID,
		"timestamp": time.Now().Unix(),
		"data":      payload,
	})
	if err != nil {
		slog.Error("webhook: failed to marshal payload", "tenant_id", tenantID, "event", event, "error", err)
		return
	}

	for _, sub := range subscriptions {
		delivery := &entities.WebhookDelivery{
			ID:        uuid.New().String(),
			TenantID:  tenantID,
			WebhookID: sub.ID,
			Event:     event.String(),
			Payload:   body,
			Status:    entities.DeliveryStatusPending,
		}
		if err := w.deliveries.Create(ctx, delivery); err != nil {
			slog.Error("webhook: failed to queue notification",
				"tenant_id", tenantID, "webhook_id", sub.ID, "event", event, "error", err)
			continue
		}
	}

	w.Trigger()
}

// Trigger asks the loop to run now rather than at the next tick.
func (w *DurableWorker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
		// A wake-up is already pending; a second one would do the same work.
	}
}

func (w *DurableWorker) Start(ctx context.Context) {
	slog.Info("webhook delivery worker started", "interval", w.interval)
	defer slog.Info("webhook delivery worker stopped")

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		delivered := w.processDue(ctx)

		// A full batch means more is waiting; go again without pausing.
		if delivered >= w.batchSize {
			continue
		}

		w.pruneIfDue(ctx)

		select {
		case <-ctx.Done():
			return
		case <-w.trigger:
		case <-ticker.C:
		}
	}
}

func (w *DurableWorker) processDue(ctx context.Context) int {
	due, err := w.deliveries.ClaimDue(ctx, w.batchSize, w.lease)
	if err != nil {
		slog.Error("webhook: failed to claim due notifications", "error", err)
		return 0
	}

	// The same reasoning as the outbox worker, and the same shape. A POST
	// cancelled in flight may already have been received and acted on, and
	// panmail cannot tell — it records a failure and delivers again, so the
	// endpoint sees the event twice. Signatures let a receiver deduplicate, but
	// making it their problem on every deploy is not a design.
	//
	// Deliveries here are sequential, so this is one request rather than the
	// two hundred the outbox had in flight. Worth the same treatment anyway:
	// it is the same bug, and it costs nothing to not have it.
	attemptCtx, cancelAttempt := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelAttempt()
	stopDrainTimer := context.AfterFunc(ctx, func() {
		time.AfterFunc(drainGrace, cancelAttempt)
	})
	defer stopDrainTimer()

	for _, d := range due {
		// Still the parent context: this decides whether to *start* another
		// delivery, and one not yet attempted cannot be duplicated by leaving
		// it. The rest stay claimed until the lease expires.
		if ctx.Err() != nil {
			return len(due)
		}
		w.attempt(attemptCtx, d)
	}
	return len(due)
}

// attempt makes one delivery and records what happened.
func (w *DurableWorker) attempt(ctx context.Context, d *entities.WebhookDelivery) {
	d.AttemptCount++

	sub, err := w.webhookUsecase.GetSubscription(ctx, d.TenantID, d.WebhookID)

	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && sub == nil):
		// Genuinely gone. A notification owed to nobody is finished, not
		// retried forever.
		w.finish(ctx, d, entities.DeliveryStatusFailed, "the webhook subscription no longer exists")
		return

	case err != nil:
		// Anything else is an operational fault, not a deleted subscription,
		// and the two must not be confused. The case that made this matter:
		// after a key rotation that missed the webhook secrets, every read
		// fails to decrypt — and treating that as "gone" permanently fails
		// every queued notification and reports a reason that is untrue. It
		// defers instead, so restoring the key recovers the queue.
		w.reschedule(ctx, d, fmt.Sprintf("the webhook subscription could not be read: %v", err))
		return
	}
	if !sub.Active {
		w.finish(ctx, d, entities.DeliveryStatusFailed, "the webhook subscription is disabled")
		return
	}

	status, err := w.post(ctx, sub, d)

	switch {
	case err != nil:
		// A transport failure is the endpoint being unreachable, which is
		// exactly the case worth retrying.
		w.reschedule(ctx, d, err.Error())

	case status >= 200 && status < 300:
		w.finish(ctx, d, entities.DeliveryStatusDelivered, "")

	case retryable(status):
		w.reschedule(ctx, d, fmt.Sprintf("endpoint returned %d", status))

	default:
		// The receiver looked at the request and rejected it. Retrying an
		// unchanged request against an unchanged opinion only keeps hitting an
		// endpoint that has already said no.
		w.finish(ctx, d, entities.DeliveryStatusFailed, fmt.Sprintf("endpoint returned %d", status))
	}
}

func (w *DurableWorker) post(ctx context.Context, sub *entities.Webhook, d *entities.WebhookDelivery) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(d.Payload))
	if err != nil {
		return 0, err
	}
	applySignature(req, sub.Secret, d.Event, d.ID, d.Payload, time.Now())

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Drained so the connection can be reused, and bounded so a receiver
	// answering with a novel cannot be used to exhaust memory here.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBodyBytes))

	return resp.StatusCode, nil
}

// reschedule puts a notification back for another attempt, or gives up if the
// schedule is exhausted.
func (w *DurableWorker) reschedule(ctx context.Context, d *entities.WebhookDelivery, reason string) {
	delay, ok := nextAttempt(d.AttemptCount)
	if !ok {
		slog.Warn("webhook: giving up on a notification",
			"tenant_id", d.TenantID, "webhook_id", d.WebhookID, "event", d.Event,
			"attempts", d.AttemptCount, "last_error", reason)
		w.finish(ctx, d, entities.DeliveryStatusFailed, reason)
		return
	}

	d.Status = entities.DeliveryStatusDeferred
	d.NextAttemptAt = time.Now().Add(delay)
	d.LastError = reason
	w.save(ctx, d)
}

func (w *DurableWorker) finish(ctx context.Context, d *entities.WebhookDelivery, status entities.DeliveryStatus, reason string) {
	d.Status = status
	d.LastError = reason
	// Kept rather than deleted, so a tenant asking why they were not told has
	// an answer. The prune below ages both out.
	w.save(ctx, d)
}

func (w *DurableWorker) save(ctx context.Context, d *entities.WebhookDelivery) {
	// Bookkeeping runs on a context derived from the worker's rather than the
	// delivery's: losing the write would leave the row claimed-then-abandoned
	// and redelivered when the lease expires.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := w.deliveries.Update(saveCtx, d); err != nil {
		slog.Error("webhook: failed to record the delivery outcome",
			"delivery_id", d.ID, "status", d.Status, "error", err)
	}
}

// pruneIfDue ages out notifications that have finished.
func (w *DurableWorker) pruneIfDue(ctx context.Context) {
	if w.retention <= 0 {
		return
	}
	if !w.lastPrune.IsZero() && time.Since(w.lastPrune) < deliveryPrunePeriod {
		return
	}
	w.lastPrune = time.Now()

	pruneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	removed, err := w.deliveries.PruneTerminal(pruneCtx, time.Now().Add(-w.retention))
	if err != nil {
		slog.Warn("webhook: failed to prune finished notifications", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("webhook: pruned finished notifications", "removed", removed)
	}
}
