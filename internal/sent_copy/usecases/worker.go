package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	"google.golang.org/protobuf/encoding/protojson"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	emailusecases "github.com/gsoultan/panmail/internal/email/usecases"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories"
)

const (
	workerInterval  = 10 * time.Second
	workerBatchSize = 20
	// claimLease is how long a claim holds before another instance may take
	// the job over. Longer than one append can take, so a slow server is not
	// mistaken for a dead worker.
	claimLease = 5 * time.Minute
	// maxAttempts bounds the retries of one copy. The last one is about a day
	// after the first, which covers a mail server being down for maintenance.
	maxAttempts = 10
	firstRetry  = time.Minute
	maxRetry    = 6 * time.Hour
)

// errPermanent marks a failure retrying cannot fix: no login for the sender,
// a request that cannot be read.
type errPermanent struct{ err error }

func (e *errPermanent) Error() string { return e.err.Error() }
func (e *errPermanent) Unwrap() error { return e.err }

func permanent(format string, args ...any) error {
	return &errPermanent{err: fmt.Errorf(format, args...)}
}

// Queue accepts messages that asked for a Sent copy. It satisfies the send
// path's SentCopyQueue.
type Queue struct {
	repo    repositories.Repository
	worker  *Worker
	now     func() time.Time
	newUUID func() string
}

var _ emailusecases.SentCopyQueue = (*Queue)(nil)

// NewQueue builds the queue. worker may be nil; when set it is woken for each
// copy queued, so a copy is filed seconds after the send rather than on the
// next tick.
func NewQueue(repo repositories.Repository, worker *Worker) *Queue {
	return &Queue{repo: repo, worker: worker, now: time.Now, newUUID: func() string { return uuid.New().String() }}
}

// Enqueue stores a copy to be filed, unless the tenant makes none.
func (q *Queue) Enqueue(ctx context.Context, tenantID, messageID string, req *panmailv1.SendEmailRequest) error {
	set, err := q.repo.GetSettings(ctx, tenantID)
	if err != nil {
		return err
	}
	if set == nil || set.Mode == entities.ModeNone || set.Mode == "" {
		return nil
	}

	raw, err := protojson.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode the Sent copy: %w", err)
	}
	if err := q.repo.EnqueueJob(ctx, &entities.Job{
		ID: q.newUUID(), TenantID: tenantID, MessageID: messageID, Request: raw,
	}, q.now()); err != nil {
		return err
	}
	if q.worker != nil {
		q.worker.Trigger()
	}
	return nil
}

// Worker files queued copies.
type Worker struct {
	repo            repositories.Repository
	appender        Appender
	providerRepo    providerStores.Repository
	providerFactory providerEntities.ProviderFactory
	now             func() time.Time
	trigger         chan struct{}
}

// NewWorker builds the worker.
func NewWorker(
	repo repositories.Repository,
	appender Appender,
	providerRepo providerStores.Repository,
	providerFactory providerEntities.ProviderFactory,
) *Worker {
	return &Worker{
		repo:            repo,
		appender:        appender,
		providerRepo:    providerRepo,
		providerFactory: providerFactory,
		now:             time.Now,
		trigger:         make(chan struct{}, 1),
	}
}

// Trigger wakes the worker without waiting for the next tick.
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
	}
}

// Start runs until ctx is done.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(workerInterval)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil && w.RunOnce(ctx) >= workerBatchSize {
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.trigger:
		}
	}
}

// RunOnce files one batch of due copies and reports how many it claimed.
func (w *Worker) RunOnce(ctx context.Context) int {
	jobs, err := w.repo.ClaimJobs(ctx, w.now(), claimLease, workerBatchSize)
	if err != nil {
		slog.Error("failed to claim Sent copies", "error", err)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		w.process(ctx, job)
	}
	return len(jobs)
}

func (w *Worker) process(ctx context.Context, job *entities.Job) {
	err := w.file(ctx, job)
	if err == nil {
		slog.Info("Sent copy filed", "id", job.MessageID, "tenant_id", job.TenantID)
		w.complete(ctx, job)
		return
	}

	attempts := job.Attempts + 1
	var perm *errPermanent
	if errors.As(err, &perm) || attempts >= maxAttempts {
		// Logged at error, because nobody else will find out: the message
		// itself was delivered, and only the sender's Sent folder is missing it.
		slog.Error("giving up on a Sent copy", "error", err, "id", job.MessageID,
			"tenant_id", job.TenantID, "attempts", attempts)
		w.complete(ctx, job)
		return
	}

	next := w.now().Add(retryDelay(attempts))
	slog.Warn("Sent copy failed, will retry", "error", err, "id", job.MessageID,
		"tenant_id", job.TenantID, "attempts", attempts, "next_attempt", next)
	if rerr := w.repo.RescheduleJob(ctx, job.ID, job.ClaimToken, attempts, next, err.Error()); rerr != nil {
		slog.Error("failed to reschedule a Sent copy", "error", rerr, "id", job.MessageID)
	}
}

func (w *Worker) complete(ctx context.Context, job *entities.Job) {
	if err := w.repo.CompleteJob(ctx, job.ID, job.ClaimToken); err != nil {
		slog.Error("failed to finish a Sent copy", "error", err, "id", job.MessageID)
	}
}

// retryDelay doubles from firstRetry, capped at maxRetry.
func retryDelay(attempts int) time.Duration {
	d := firstRetry
	for i := 1; i < attempts && d < maxRetry; i++ {
		d *= 2
	}
	return min(d, maxRetry)
}

// file makes the copy as the tenant's current setting says. The setting is
// read now rather than when the copy was queued, so switching to None stops
// copies already waiting.
func (w *Worker) file(ctx context.Context, job *entities.Job) error {
	set, err := w.repo.GetSettings(ctx, job.TenantID)
	if err != nil {
		return err
	}
	if set == nil || set.Mode == entities.ModeNone || set.Mode == "" {
		return nil
	}

	req := &panmailv1.SendEmailRequest{}
	if err := protojson.Unmarshal(job.Request, req); err != nil {
		return permanent("read the queued Sent copy: %w", err)
	}
	from, err := gsmail.ParseEmailAddress(req.GetFrom())
	if err != nil {
		return permanent("the sender %q is not an address: %w", req.GetFrom(), err)
	}
	sender := entities.NormaliseAddress(from.Address)
	msg := emailusecases.SentCopyEmail(req)

	switch set.Mode {
	case entities.ModeBccSender:
		return w.mailToSender(ctx, job.TenantID, req.GetProviderId(), msg, sender)

	case entities.ModeSharedLogin:
		raw, err := gsmail.RenderMessage(msg)
		if err != nil {
			return permanent("render the Sent copy: %w", err)
		}
		return w.appender.Append(ctx, set, set.MasterLogin(sender), set.MasterPassword, raw, job.CreatedAt)

	case entities.ModePerUserLogin:
		mailbox, err := w.repo.GetMailbox(ctx, job.TenantID, sender)
		if err != nil {
			return err
		}
		if mailbox == nil {
			return permanent("no mailbox login is stored for %s", sender)
		}
		raw, err := gsmail.RenderMessage(msg)
		if err != nil {
			return permanent("render the Sent copy: %w", err)
		}
		return w.appender.Append(ctx, set, mailbox.Login(), mailbox.Password, raw, job.CreatedAt)

	default:
		return permanent("unknown Sent copies mode %q", set.Mode)
	}
}

// mailToSender sends the copy to the sender through the message's own
// provider, with only the sender in the envelope.
func (w *Worker) mailToSender(ctx context.Context, tenantID, providerID string, msg gsmail.Email, sender string) error {
	provider, err := w.providerRepo.GetByID(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	if provider == nil {
		return permanent("the provider %s no longer exists", providerID)
	}
	s, err := w.providerFactory.CreateSender(provider)
	if err != nil {
		return err
	}
	if provider.Type == panmailv1.ProviderType_PROVIDER_TYPE_SMTP {
		msg.Envelope = []string{sender}
	} else {
		// A vendor API cannot separate the envelope from the headers and
		// would deliver to everyone named in them, so the copy is addressed
		// to the sender alone.
		msg.To, msg.Cc = []string{sender}, nil
	}
	return s.Send(ctx, msg)
}
