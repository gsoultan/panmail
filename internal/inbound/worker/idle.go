package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// IdleSupervisor holds an open IMAP connection per provider so mail arrives
// when the server says so rather than up to a poll interval later.
//
// It runs alongside the poller, not instead of it, and that is the important
// part. A hung IDLE is silent: the connection looks open, the server has
// nothing to say, and inbound simply stops with nothing in the logs. The poll
// is the floor under that — slower, but it cannot fail quietly in the same
// way. POP3 has no IDLE at all, so those providers are the poller's regardless.
//
// Duplicate delivery between the two paths is expected and harmless: inbound
// processing identifies a message by its own Message-ID, so whichever path
// sees it first wins and the other is a no-op.
type IdleSupervisor struct {
	poller *Poller

	// How long to wait before reconnecting after a failure, and the ceiling
	// that backoff climbs to.
	retryDelay time.Duration
	maxDelay   time.Duration

	// How often the provider list is re-read, so a provider added or removed
	// while running is picked up.
	refresh time.Duration

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

const (
	defaultIdleRetryDelay = 5 * time.Second
	defaultIdleMaxDelay   = 5 * time.Minute
	defaultIdleRefresh    = time.Minute
)

func NewIdleSupervisor(poller *Poller) *IdleSupervisor {
	return &IdleSupervisor{
		poller:     poller,
		retryDelay: defaultIdleRetryDelay,
		maxDelay:   defaultIdleMaxDelay,
		refresh:    defaultIdleRefresh,
		running:    map[string]context.CancelFunc{},
	}
}

// Start supervises one IDLE session per IMAP provider until ctx is done.
func (s *IdleSupervisor) Start(ctx context.Context) {
	slog.Info("imap idle supervisor started")
	defer slog.Info("imap idle supervisor stopped")

	ticker := time.NewTicker(s.refresh)
	defer ticker.Stop()

	for {
		s.reconcile(ctx)

		select {
		case <-ctx.Done():
			s.stopAll()
			return
		case <-ticker.C:
		}
	}
}

// reconcile starts a session for every IMAP provider that has none, and stops
// sessions for providers that have gone away.
func (s *IdleSupervisor) reconcile(ctx context.Context) {
	wanted := map[string]struct{}{}

	tenants, _, err := s.poller.tenantRepo.List(ctx, 1000, "")
	if err != nil {
		// Leave the existing sessions alone. Tearing them down because the
		// tenant list could not be read would stop inbound over a transient
		// database error.
		slog.Error("idle: failed to list tenants", "error", err)
		return
	}

	for _, tenant := range tenants {
		providers, _, err := s.poller.providerRepo.List(ctx, tenant.ID, "", "", 1000, "")
		if err != nil {
			slog.Error("idle: failed to list providers", "tenant_id", tenant.ID, "error", err)
			continue
		}
		for _, provider := range providers {
			// POP3 has no IDLE; those stay with the poller.
			if provider.Type != panmailv1.ProviderType_PROVIDER_TYPE_IMAP {
				continue
			}
			wanted[provider.ID] = struct{}{}
			s.ensure(ctx, tenant.ID, provider)
		}
	}

	s.stopUnwanted(wanted)
}

func (s *IdleSupervisor) ensure(ctx context.Context, tenantID string, provider *providerEntities.EmailProvider) {
	s.mu.Lock()
	if _, already := s.running[provider.ID]; already {
		s.mu.Unlock()
		return
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s.running[provider.ID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, provider.ID)
			s.mu.Unlock()
			cancel()
		}()
		s.session(sessionCtx, tenantID, provider)
	}()
}

// session keeps one provider connected, reconnecting with backoff.
func (s *IdleSupervisor) session(ctx context.Context, tenantID string, provider *providerEntities.EmailProvider) {
	delay := s.retryDelay

	for ctx.Err() == nil {
		delivered, err := s.idleOnce(ctx, tenantID, provider)

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			slog.Warn("idle: session ended with an error",
				"tenant_id", tenantID, "provider_id", provider.ID, "error", err, "retry_in", delay)
		}

		// A session that delivered something was working, so the next failure
		// starts from the short delay again. Without this a server that drops
		// the connection after every message climbs to the ceiling and stays
		// there while behaving perfectly well.
		if delivered {
			delay = s.retryDelay
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		if delay < s.maxDelay {
			delay *= 2
			if delay > s.maxDelay {
				delay = s.maxDelay
			}
		}
	}
}

// idleOnce runs a single IDLE session and reports whether anything arrived.
func (s *IdleSupervisor) idleOnce(ctx context.Context, tenantID string, provider *providerEntities.EmailProvider) (bool, error) {
	receiverObj, err := s.poller.providerFactory.CreateReceiver(provider)
	if err != nil {
		return false, err
	}

	// Idle is part of gsmail.Receiver, so no assertion is needed. A receiver
	// whose protocol has no IDLE reports an error and the backoff below keeps
	// the retries from becoming a spin.
	emails, errs := receiverObj.Idle(ctx)

	delivered := false
	for {
		select {
		case <-ctx.Done():
			return delivered, nil

		case e, open := <-emails:
			if !open {
				// The channel closing is the session ending; whatever the
				// reason, reconnecting is the answer.
				return delivered, nil
			}
			delivered = true
			s.deliver(ctx, tenantID, provider, e)

		case err, open := <-errs:
			if !open {
				return delivered, nil
			}
			if err != nil {
				return delivered, err
			}
		}
	}
}

func (s *IdleSupervisor) deliver(ctx context.Context, tenantID string, provider *providerEntities.EmailProvider, e gsmail.Email) {
	headers := e.Headers
	if headers == nil {
		headers = make(map[string]string)
	}

	inbound := &panmailv1.InboundEmail{
		// The same identity the poller derives, which is what makes the two
		// paths safe to run together: whichever sees the message first wins
		// and the other is a no-op.
		Id:        messageIdentity(tenantID, provider.ID, e),
		TenantId:  tenantID,
		From:      e.From,
		To:        e.To,
		Subject:   e.Subject,
		BodyHtml:  string(e.HTMLBody),
		BodyText:  string(e.Body),
		Timestamp: timestamppb.New(receivedAt(e)),
		Headers:   headers,
	}

	if err := s.poller.inboundUsecase.Process(ctx, inbound); err != nil {
		slog.Error("idle: failed to process inbound email",
			"tenant_id", tenantID, "provider_id", provider.ID, "error", err)
	}
}

func (s *IdleSupervisor) stopUnwanted(wanted map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.running {
		if _, keep := wanted[id]; !keep {
			slog.Info("idle: provider no longer present, closing session", "provider_id", id)
			cancel()
		}
	}
}

func (s *IdleSupervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.running {
		cancel()
	}
}

// Sessions reports how many providers are currently held open, for tests and
// for a health check that wants to notice inbound going quiet.
func (s *IdleSupervisor) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}
