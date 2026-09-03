package usecases

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	suppressionusecases "github.com/gsoultan/panmail/internal/suppression/usecases"
	tenantusecases "github.com/gsoultan/panmail/internal/tenant/usecases"
	"github.com/gsoultan/panmail/pkg/cache"
	"github.com/gsoultan/panmail/pkg/emailutil"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	outboxBatchSize    = 500
	outboxConcurrency  = 200
	perEmailTimeout    = 30 * time.Second
	bookkeepingTimeout = 10 * time.Second
	retryPatternTTL    = time.Minute

	// Short and few: this runs after a send, and the lease is the backstop.
	// The point is to ride out a blip, not to block a worker.
	bookkeepingAttempts = 3
	bookkeepingBackoff  = 200 * time.Millisecond

	// How long a send that is already in progress gets to finish after
	// shutdown begins. Long enough for an SMTP exchange that has started, and
	// short enough to sit inside both the worker drain timeout and a default
	// Kubernetes termination grace period — a drain that outlasts either is
	// not a drain, it is a SIGKILL with extra steps.
	drainGrace = 10 * time.Second
)

var defaultRetryPattern = []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"}

type QueueWorker interface {
	Start(ctx context.Context)
	Trigger()

	// SetRetention configures how long a permanently failed message is kept
	// before the worker prunes it. Zero, the default, disables pruning.
	SetRetention(d time.Duration)

	// SetRetryPatternSource installs the source of the deployment-wide backoff
	// schedule. Called during wiring, before the worker starts.
	SetRetryPatternSource(src RetryPatternSource)
}

type queueWorker struct {
	// How long a permanently failed message is kept, in nanoseconds. Zero
	// disables pruning, which is what a deployment that has not configured it
	// gets — deleting someone's delivery history because a default said so
	// would be worse than the table growing.
	//
	// Atomic because the retention worker rewrites it whenever an
	// administrator saves the settings page, while this worker is reading it
	// between batches.
	retention atomic.Int64
	lastPrune time.Time

	outboxRepo         stores.OutboxRepository
	emailUsecase       SendEmailUsecase
	suppressionUsecase suppressionusecases.ManageSuppressionsUsecase
	tenantUsecase      tenantusecases.TenantUsecase
	interval           time.Duration
	trigger            chan struct{}

	retryPatterns *cache.TTLCache[[]string]

	// Where the deployment-wide backoff schedule comes from. Read on use
	// rather than captured at construction: it lives in the database now, so a
	// change saved on the settings page reaches this worker — on any instance —
	// without a restart. Nil means the compiled default.
	retryPatternSource RetryPatternSource
}

// RetryPatternSource supplies the deployment-wide backoff schedule.
//
// Narrow on purpose so the worker cannot write settings, and so a test can
// supply a pattern without a database. system_settings.Provider satisfies it.
type RetryPatternSource interface {
	RetryPattern() []string
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
		retryPatterns:      cache.New[[]string](retryPatternTTL),
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
	defer slog.Info("queue worker stopped")

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		count := w.processPending(ctx)

		// A full batch means more is waiting; go again without pausing.
		if count >= outboxBatchSize {
			continue
		}

		// Only once the queue is drained, and at most hourly. Pruning is
		// housekeeping: it must never delay a message, and running it on the
		// send tick would issue a delete scan every few seconds for a table
		// that changes by the day.
		w.pruneIfDue(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.trigger:
		}
	}
}

func (w *queueWorker) processPending(ctx context.Context) int {
	emails, err := w.outboxRepo.ClaimPending(ctx, outboxBatchSize, perEmailTimeout)
	if err != nil {
		slog.Error("failed to claim pending outbox emails", "error", err)
		return 0
	}

	count := len(emails)
	if count == 0 {
		return 0
	}

	slog.Info("queue worker processing batch", "count", count)

	// A send that has started must be allowed to finish.
	//
	// Shutting down used to cancel this context, which cancelled every send
	// with it — mid-SMTP, mid-conversation with a provider. A deploy signalled
	// while the queue was busy cut off everything in flight: measured at 188
	// messages abandoned and 184 more deferred out of one signal. Nothing was
	// lost, because the lease brings a claimed row back, but the cost is worse
	// than a delay. A send cancelled after DATA was accepted and before the
	// reply arrived has been delivered, and panmail cannot know it, so it goes
	// out again. Every deploy was a chance to send the same email twice.
	//
	// So the batch runs on a context shutdown does not reach, with a bounded
	// grace once it starts: in-flight sends finish, and anything still going
	// after that is cut rather than holding the process open.
	batchCtx, cancelBatch := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBatch()
	stopDrainTimer := context.AfterFunc(ctx, func() {
		slog.Info("draining in-flight sends before shutdown", "grace", drainGrace)
		time.AfterFunc(drainGrace, cancelBatch)
	})
	defer stopDrainTimer()

	var wg sync.WaitGroup
	sem := make(chan struct{}, outboxConcurrency)

	for _, e := range emails {
		wg.Add(1)
		go func(email *entities.OutboxEmail) {
			defer wg.Done()
			defer recoverEmail(email.ID)

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				w.processEmail(batchCtx, email)
			// Deliberately the parent context, not the batch one: a message
			// that has not started has not been attempted, so dropping it
			// risks nothing. It stays claimed, the lease expires, and it is
			// picked up again. Waiting for the whole batch would turn every
			// shutdown into a full drain of 500 messages.
			case <-ctx.Done():
			}
		}(e)
	}
	wg.Wait()

	return count
}

// recoverEmail keeps one malformed message from taking the process down. An
// unrecovered panic in any goroutine terminates the whole gateway.
func recoverEmail(id string) {
	if r := recover(); r != nil {
		slog.Error("recovered from panic while processing outbox email", "id", id, "panic", r)
	}
}

func (w *queueWorker) processEmail(ctx context.Context, e *entities.OutboxEmail) {
	sendCtx, cancel := context.WithTimeout(ctx, perEmailTimeout)
	defer cancel()

	slog.Info("worker processing email", "id", e.ID, "tenant_id", e.TenantID, "retry_count", e.RetryCount)

	var req panmailv1.SendEmailRequest
	if err := protojson.Unmarshal(e.Request, &req); err != nil {
		slog.Error("failed to unmarshal outbox email request", "error", err, "id", e.ID)
		w.deleteOutbox(ctx, e.ID)
		return
	}

	sendCtx = context.WithValue(sendCtx, SkipOutboxKey, true)
	sendCtx = context.WithValue(sendCtx, MessageIDKey, e.ID)

	res, err := w.emailUsecase.SendEmail(sendCtx, e.TenantID, &req)
	if err == nil && res != nil {
		slog.Info("email sent successfully from worker", "id", e.ID)
		w.deleteOutbox(ctx, e.ID)
		return
	}
	if err == nil {
		err = fmt.Errorf("send returned no result")
	}

	w.handleFailure(ctx, e, &req, err)
}

// handleFailure decides whether to retry, and records the outcome. Bookkeeping
// runs on a context derived from the worker's, not from the send's — the send
// context may already have timed out, and losing the status write would leave
// the row claimed-then-abandoned and resent forever.
func (w *queueWorker) handleFailure(ctx context.Context, e *entities.OutboxEmail, req *panmailv1.SendEmailRequest, sendErr error) {
	bookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()

	slog.Warn("email delivery attempt failed", "id", e.ID, "error", sendErr)

	e.RetryCount++
	e.UpdatedAt = time.Now()
	e.LastError = sendErr.Error()

	classification := emailutil.ClassifyError(e.LastError)
	recipients := uniqueRecipients(req.To, req.Cc, req.Bcc)
	retryPattern := w.getRetryPattern(bookCtx, e.TenantID)

	if delay, ok := nextRetryDelay(classification, e.RetryCount, retryPattern); ok {
		e.Status = entities.OutboxStatusDeferred
		e.NextRetryAt = time.Now().Add(delay)
		slog.Info("email delivery deferred for retry",
			"id", e.ID, "retry_count", e.RetryCount, "next_retry_at", e.NextRetryAt)

		w.recordForEach(bookCtx, e, req, recipients, panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED)
		w.updateOutbox(bookCtx, e)
		return
	}

	e.Status = entities.OutboxStatusFailed
	slog.Info("email delivery failed permanently",
		"id", e.ID, "type", classification.Type.String(),
		"recipient_at_fault", classification.RecipientAtFault, "error", e.LastError)

	w.recordForEach(bookCtx, e, req, recipients, classification.Type)

	// Suppress only when the provider told us something about the address
	// itself. A connection, credential or policy failure applies to the send,
	// not to the people being written to, and suppressing them on that basis
	// silently destroys a tenant's ability to reach their own customers.
	if classification.RecipientAtFault {
		w.suppress(bookCtx, e, recipients, classification)
	}

	w.updateOutbox(bookCtx, e)
}

// nextRetryDelay reports the delay before the next attempt, and whether to
// retry at all.
func nextRetryDelay(c emailutil.Classification, retryCount int, pattern []string) (time.Duration, bool) {
	if !c.Retryable || retryCount > len(pattern) {
		return 0, false
	}

	d, err := time.ParseDuration(pattern[retryCount-1])
	if err != nil {
		// Fall back to a quadratic backoff rather than dropping the message
		// because an operator mistyped a duration.
		return time.Duration(retryCount*retryCount) * time.Minute, true
	}
	return d, true
}

func (w *queueWorker) recordForEach(
	ctx context.Context,
	e *entities.OutboxEmail,
	req *panmailv1.SendEmailRequest,
	recipients []string,
	eventType panmailv1.EmailEventType,
) {
	for _, recipient := range recipients {
		if err := w.emailUsecase.RecordEvent(ctx, e.TenantID, req.ProviderId, e.ID, eventType, recipient, req.Subject, e.LastError, nil); err != nil {
			slog.Error("failed to record delivery event", "error", err, "id", e.ID, "recipient", recipient)
		}
	}
}

func (w *queueWorker) suppress(ctx context.Context, e *entities.OutboxEmail, recipients []string, c emailutil.Classification) {
	reason := fmt.Sprintf("Automatic suppression due to %s: %s", c.Type.String(), e.LastError)

	for _, recipient := range recipients {
		if _, err := w.suppressionUsecase.Add(ctx, e.TenantID, &panmailv1.AddSuppressionRequest{
			Email:  recipient,
			Reason: reason,
		}); err != nil {
			slog.Error("failed to suppress recipient", "error", err, "recipient", recipient, "id", e.ID)
		}
	}
}

func (w *queueWorker) updateOutbox(ctx context.Context, e *entities.OutboxEmail) {
	bookkeep(ctx, "update outbox email status", e.ID, func(c context.Context) error {
		return w.outboxRepo.Update(c, e)
	})
}

func (w *queueWorker) deleteOutbox(ctx context.Context, id string) {
	bookkeep(ctx, "delete outbox email", id, func(c context.Context) error {
		return w.outboxRepo.Delete(c, id)
	})
}

// bookkeep runs the write that records what has already happened to a message.
//
// By the time this runs the send is over, so a failure here does not fail the
// message — it loses the record of it. The row keeps its old state, the lease
// expires, another worker claims it, and the recipient gets a second copy of an
// email that was already delivered.
//
// One attempt turned out not to be enough. Two gateways against one database
// exhausted the server's connections, every write in that window failed with
// "sorry, too many clients already", and the duplicates showed up at the far
// end. The connection bound in pkg/db is the fix for that particular cause;
// this is the fix for the shape of it, because any transient database error has
// the same consequence. A few short retries turn a hiccup into a non-event.
//
// It is still at-least-once — nothing here can make it exactly-once, since the
// send and the record of it are not one transaction — but the window shrinks
// from "one failed query" to "the database was unreachable for a second".
func bookkeep(ctx context.Context, what, id string, write func(context.Context) error) {
	// WithoutCancel, because a cancelled batch is exactly when this matters:
	// on shutdown the send has happened and the row still has to be settled.
	base := context.WithoutCancel(ctx)

	var err error
	for attempt := range bookkeepingAttempts {
		if attempt > 0 {
			time.Sleep(bookkeepingBackoff << (attempt - 1))
		}

		attemptCtx, cancel := context.WithTimeout(base, bookkeepingTimeout)
		err = write(attemptCtx)
		cancel()

		if err == nil {
			if attempt > 0 {
				slog.Warn("outbox bookkeeping succeeded on retry",
					"operation", what, "id", id, "attempts", attempt+1)
			}
			return
		}
	}

	// Naming the consequence, because "failed to delete outbox email" reads as
	// a stale row and the real cost is a second copy in someone's inbox.
	slog.Error("failed to "+what+"; the message may be sent again when the lease expires",
		"error", err, "id", id, "attempts", bookkeepingAttempts)
}

// SetRetryPatternSource installs the source of the deployment-wide schedule.
// Called during wiring, before the worker starts.
func (w *queueWorker) SetRetryPatternSource(src RetryPatternSource) {
	w.retryPatternSource = src
}

// globalPattern is the schedule for a tenant that has not set its own. An
// unset or empty source means the compiled default rather than no retries at
// all — a deployment that never opened the settings page still retries.
func (w *queueWorker) globalPattern() []string {
	if w.retryPatternSource != nil {
		if pattern := w.retryPatternSource.RetryPattern(); len(pattern) > 0 {
			return pattern
		}
	}
	return defaultRetryPattern
}

func (w *queueWorker) getRetryPattern(ctx context.Context, tenantID string) []string {
	if pattern, ok := w.retryPatterns.Get(tenantID); ok {
		return pattern
	}

	pattern := w.globalPattern()
	if tenant, err := w.tenantUsecase.GetTenantByID(ctx, tenantID); err == nil && tenant != nil && len(tenant.RetryPattern) > 0 {
		pattern = tenant.RetryPattern
	}

	w.retryPatterns.Put(tenantID, pattern)
	return pattern
}

// How often the terminal-row sweep runs, at most.
const prunePeriod = time.Hour

// pruneIfDue removes failed messages older than the configured retention.
//
// Runs on the worker rather than as its own goroutine so it cannot overlap a
// send batch, and only when the queue is already drained: housekeeping must
// never be the reason mail is late.
func (w *queueWorker) pruneIfDue(ctx context.Context) {
	retention := time.Duration(w.retention.Load())
	if retention <= 0 {
		return
	}
	if !w.lastPrune.IsZero() && time.Since(w.lastPrune) < prunePeriod {
		return
	}
	w.lastPrune = time.Now()

	pruneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()

	cutoff := time.Now().Add(-retention)
	removed, err := w.outboxRepo.PruneTerminal(pruneCtx, cutoff)
	if err != nil {
		// Housekeeping failing is not a reason to stop sending; it will be
		// retried on the next period.
		slog.Warn("failed to prune terminal outbox rows", "error", err, "cutoff", cutoff)
		return
	}
	if removed > 0 {
		slog.Info("pruned terminal outbox rows", "removed", removed, "older_than", cutoff)
	}
}

// SetRetention configures how long a permanently failed message is kept. Safe
// to call while the worker is running.
func (w *queueWorker) SetRetention(d time.Duration) { w.retention.Store(int64(d)) }
