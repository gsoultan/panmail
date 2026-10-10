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
	"sync"
	"sync/atomic"
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
	// How many subscriptions are delivered to at once, and how long into a
	// claim new attempts may still be started. See processDue.
	concurrency int
	batchBudget time.Duration
	// Time held back at the end of a lease for recording an outcome, so an
	// attempt started at the last moment still writes it under the claim.
	saveReserve time.Duration
	// How long a finished notification is kept, in nanoseconds. Atomic
	// because the retention worker rewrites it whenever an administrator
	// saves the settings page, while this worker is reading it between
	// batches.
	retention atomic.Int64

	// trigger wakes the loop when something is enqueued, so a notification
	// does not wait out the poll interval before its first attempt.
	trigger   chan struct{}
	lastPrune time.Time
}

const (
	defaultDeliveryInterval = 5 * time.Second
	defaultDeliveryBatch    = 50
	defaultDeliveryTimeout  = 15 * time.Second

	// A claim has to outlast everything done under it, or another worker
	// takes the rows over while this one is still posting them and the
	// endpoint receives each notification twice. The batch used to be fifty
	// sequential POSTs of up to fifteen seconds each -- twelve and a half
	// minutes -- under a two-minute lease; with two workers running, most of
	// a slow batch was delivered twice.
	//
	// Now no attempt starts more than defaultBatchBudget into a claim, and
	// one that starts then finishes within defaultDeliveryTimeout and records
	// its outcome within defaultSaveReserve. The lease covers all three with
	// room to spare, and TestTheLeaseOutlastsTheWorstCaseBatch holds it to
	// that. Rows a batch did not reach are released for the next claim.
	defaultBatchBudget   = time.Minute
	defaultSaveReserve   = 10 * time.Second
	defaultDeliveryLease = 2 * time.Minute

	// Subscriptions delivered to at once. Each subscription's notifications
	// are still sent one at a time and in order; this is what stops one slow
	// endpoint from holding every other tenant's notifications behind it.
	defaultDeliveryConcurrency = 8

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
	w := &DurableWorker{
		deliveries:     deliveries,
		webhookUsecase: webhookUsecase,
		client:         &http.Client{Timeout: defaultDeliveryTimeout},
		interval:       defaultDeliveryInterval,
		batchSize:      defaultDeliveryBatch,
		lease:          defaultDeliveryLease,
		concurrency:    defaultDeliveryConcurrency,
		batchBudget:    defaultBatchBudget,
		saveReserve:    defaultSaveReserve,
		trigger:        make(chan struct{}, 1),
	}
	w.SetRetention(defaultDeliveryRetention)
	return w
}

// SetRetention configures how long a finished notification is kept before it
// is pruned. Zero disables pruning. Safe to call while the worker is running.
func (w *DurableWorker) SetRetention(d time.Duration) { w.retention.Store(int64(d)) }

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
	claimedAt := time.Now()
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
	attemptCtx, cancelAttempt := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelAttempt()
	stopDrainTimer := context.AfterFunc(ctx, func() {
		time.AfterFunc(drainGrace, cancelAttempt)
	})
	defer stopDrainTimer()

	startBy := claimedAt.Add(w.startWindow())

	// One subscription's notifications go out one at a time and in order;
	// different subscriptions go out side by side, up to w.concurrency. The
	// batch was sequential, so an endpoint taking fifteen seconds to answer
	// held up every notification queued behind it, for every tenant.
	sem := make(chan struct{}, max(w.concurrency, 1))
	var wg sync.WaitGroup
	for _, group := range bySubscription(due) {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			for i, d := range group {
				// Still the parent context: this decides whether to *start*
				// another delivery, and one not yet attempted cannot be
				// duplicated by leaving it. Nor may one start so late in the
				// claim that the lease could run out before it is recorded.
				if ctx.Err() != nil || !time.Now().Before(startBy) {
					w.release(ctx, group[i:])
					return
				}
				w.attempt(attemptCtx, d)
			}
		})
	}
	wg.Wait()
	return len(due)
}

// startWindow is how long into a claim an attempt may still start: the batch
// budget, or less if the lease is too short to fit an attempt and its
// bookkeeping after it.
func (w *DurableWorker) startWindow() time.Duration {
	window := w.lease - w.client.Timeout - w.saveReserve
	if w.client.Timeout == 0 {
		// An unbounded client cannot be fitted into any lease. Not how the
		// worker is built, but a zero here should not mean "start nothing".
		window = w.lease - w.saveReserve
	}
	return min(w.batchBudget, window)
}

// bySubscription groups a batch by the subscription it is owed to, keeping
// each group in claim order.
func bySubscription(due []*entities.WebhookDelivery) [][]*entities.WebhookDelivery {
	index := map[string]int{}
	var groups [][]*entities.WebhookDelivery
	for _, d := range due {
		i, ok := index[d.WebhookID]
		if !ok {
			i = len(groups)
			index[d.WebhookID] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], d)
	}
	return groups
}

// release hands back notifications this batch claimed but did not attempt,
// so the next claim -- here or on another instance -- can take them straight
// away rather than waiting out the lease. Nothing about them changes but the
// claim.
func (w *DurableWorker) release(ctx context.Context, rest []*entities.WebhookDelivery) {
	for _, d := range rest {
		d.Status = entities.DeliveryStatusDeferred
		if d.AttemptCount == 0 {
			d.Status = entities.DeliveryStatusPending
		}
		w.save(ctx, d)
	}
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

	err := w.deliveries.Update(saveCtx, d)
	switch {
	case errors.Is(err, stores.ErrClaimLost):
		// The lease ran out and another worker has the row. Its outcome is
		// the one that counts; overwriting it is how a delivered
		// notification used to go back on the queue.
		slog.Warn("webhook: claim lost before the outcome was recorded",
			"delivery_id", d.ID, "status", d.Status)
	case err != nil:
		slog.Error("webhook: failed to record the delivery outcome",
			"delivery_id", d.ID, "status", d.Status, "error", err)
	}
}

// pruneIfDue ages out notifications that have finished.
func (w *DurableWorker) pruneIfDue(ctx context.Context) {
	retention := time.Duration(w.retention.Load())
	if retention <= 0 {
		return
	}
	if !w.lastPrune.IsZero() && time.Since(w.lastPrune) < deliveryPrunePeriod {
		return
	}
	w.lastPrune = time.Now()

	pruneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	removed, err := w.deliveries.PruneTerminal(pruneCtx, time.Now().Add(-retention))
	if err != nil {
		slog.Warn("webhook: failed to prune finished notifications", "error", err)
		return
	}
	if removed > 0 {
		slog.Info("webhook: pruned finished notifications", "removed", removed)
	}
}
