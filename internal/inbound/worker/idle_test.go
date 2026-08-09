package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	inboundUsecases "github.com/gsoultan/panmail/internal/inbound/usecases"
	tenantEntities "github.com/gsoultan/panmail/internal/tenant/entities"
	tenantRepositories "github.com/gsoultan/panmail/internal/tenant/repositories"
)

// A hung or dropped IDLE is the failure that matters: the connection looks
// open, the server has nothing to say, and inbound stops with nothing in the
// logs. These cover the supervisor noticing and reconnecting.

// --- fakes ---

type fakeTenants struct {
	tenantRepositories.TenantRepository
	// Guarded because the supervisor reads this from its own goroutine while a
	// test changes it.
	mu      sync.Mutex
	tenants []*tenantEntities.Tenant
	err     error
}

func (f *fakeTenants) List(context.Context, int, string) ([]*tenantEntities.Tenant, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tenants, "", f.err
}

func (f *fakeTenants) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

type fakeProviders struct {
	providerStores.Repository
	mu        sync.Mutex
	providers []*providerEntities.EmailProvider
}

func (f *fakeProviders) List(context.Context, string, string, string, int, string) ([]*providerEntities.EmailProvider, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*providerEntities.EmailProvider(nil), f.providers...), "", nil
}

func (f *fakeProviders) set(ps ...*providerEntities.EmailProvider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = ps
}

type recordingUsecase struct {
	inboundUsecases.InboundUsecase
	mu   sync.Mutex
	seen []string
}

func (r *recordingUsecase) Process(_ context.Context, e *panmailv1.InboundEmail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, e.Subject)
	return nil
}

func (r *recordingUsecase) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

// A receiver that idles, driven by the test.
type fakeIdler struct {
	gsmail.Receiver
	mu       sync.Mutex
	sessions int
	// deliver is called per session to produce the messages it emits and the
	// error it ends with.
	deliver func(session int) ([]gsmail.Email, error)
}

func (f *fakeIdler) Idle(ctx context.Context) (<-chan gsmail.Email, <-chan error) {
	f.mu.Lock()
	f.sessions++
	session := f.sessions
	f.mu.Unlock()

	emails := make(chan gsmail.Email, 4)
	errs := make(chan error, 1)

	go func() {
		defer close(emails)
		defer close(errs)

		msgs, err := f.deliver(session)
		for _, m := range msgs {
			select {
			case emails <- m:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			select {
			case errs <- err:
			case <-ctx.Done():
			}
		}
	}()

	return emails, errs
}

func (f *fakeIdler) sessionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions
}

type fakeFactory struct {
	providerEntities.ProviderFactory
	receiver gsmail.Receiver
	err      error
}

func (f *fakeFactory) CreateReceiver(*providerEntities.EmailProvider) (gsmail.Receiver, error) {
	return f.receiver, f.err
}

// --- helpers ---

func imapProvider(id string) *providerEntities.EmailProvider {
	return &providerEntities.EmailProvider{
		ID: id, TenantID: tenantA, Name: "Inbox",
		Type: panmailv1.ProviderType_PROVIDER_TYPE_IMAP,
	}
}

func newSupervisor(t *testing.T, providers *fakeProviders, factory *fakeFactory, usecase *recordingUsecase) *IdleSupervisor {
	t.Helper()
	poller := NewPoller(
		&fakeTenants{tenants: []*tenantEntities.Tenant{{ID: tenantA}}},
		providers, usecase, factory, time.Hour,
	)
	s := NewIdleSupervisor(poller)
	// Wound right down so the test does not sit through real backoff.
	s.retryDelay = time.Millisecond
	s.maxDelay = 5 * time.Millisecond
	s.refresh = 5 * time.Millisecond
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// --- tests ---

func TestAnArrivingMessageIsProcessed(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"))
	usecase := &recordingUsecase{}
	idler := &fakeIdler{deliver: func(session int) ([]gsmail.Email, error) {
		if session > 1 {
			// Keep later sessions quiet so the count stays meaningful.
			return nil, nil
		}
		return []gsmail.Email{{Subject: "Hello", Headers: map[string]string{"Message-ID": "<a@x>"}}}, nil
	}}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, usecase)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the message to be processed", func() bool { return usecase.count() >= 1 })
}

// The whole reason the supervisor exists rather than a bare Idle call.
func TestASessionThatEndsIsReopened(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) {
		return nil, errors.New("connection reset")
	}}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, &recordingUsecase{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the session to be reopened", func() bool { return idler.sessionCount() >= 3 })
}

func TestOneSessionPerProvider(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"), imapProvider("p2"))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) {
		// Hold the session open so reconcile runs repeatedly against it.
		select {}
	}}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, &recordingUsecase{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "both providers to be connected", func() bool { return s.Sessions() == 2 })

	// Reconcile runs every few milliseconds; a provider already connected must
	// not be connected again.
	time.Sleep(40 * time.Millisecond)
	if got := s.Sessions(); got != 2 {
		t.Errorf("holding %d sessions for 2 providers", got)
	}
}

func TestAProviderThatGoesAwayIsDisconnected(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"), imapProvider("p2"))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { select {} }}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, &recordingUsecase{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "both to connect", func() bool { return s.Sessions() == 2 })

	providers.set(imapProvider("p1"))
	eventually(t, "the removed provider to be dropped", func() bool { return s.Sessions() == 1 })
}

// POP3 has no IDLE, so those providers belong to the poller and must not be
// retried forever here.
func TestPop3ProvidersAreLeftToThePoller(t *testing.T) {
	pop3 := imapProvider("p1")
	pop3.Type = panmailv1.ProviderType_PROVIDER_TYPE_POP3

	providers := &fakeProviders{}
	providers.set(pop3)
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { return nil, nil }}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, &recordingUsecase{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	time.Sleep(40 * time.Millisecond)
	if s.Sessions() != 0 {
		t.Error("a POP3 provider was given an IDLE session")
	}
	if idler.sessionCount() != 0 {
		t.Error("IDLE was attempted against POP3")
	}
}

func TestEverythingStopsWhenTheContextIsCancelled(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { select {} }}

	s := newSupervisor(t, providers, &fakeFactory{receiver: idler}, &recordingUsecase{})
	ctx, cancel := context.WithCancel(context.Background())
	go s.Start(ctx)

	eventually(t, "the session to open", func() bool { return s.Sessions() == 1 })

	cancel()
	// A worker that does not stop holds shutdown open past its drain timeout.
	eventually(t, "sessions to close", func() bool { return s.Sessions() == 0 })
}

// Tearing down working sessions because the tenant list could not be read
// would stop inbound over a transient database error.
func TestATransientListFailureLeavesSessionsAlone(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(imapProvider("p1"))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { select {} }}
	tenants := &fakeTenants{tenants: []*tenantEntities.Tenant{{ID: tenantA}}}

	poller := NewPoller(tenants, providers, &recordingUsecase{}, &fakeFactory{receiver: idler}, time.Hour)
	s := NewIdleSupervisor(poller)
	s.retryDelay, s.maxDelay, s.refresh = time.Millisecond, 5*time.Millisecond, 5*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the session to open", func() bool { return s.Sessions() == 1 })

	tenants.fail(errors.New("database unreachable"))
	time.Sleep(40 * time.Millisecond)

	if s.Sessions() != 1 {
		t.Error("a working session was torn down because the tenant list could not be read")
	}
}
