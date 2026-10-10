package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/gsmail"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	tenantEntities "github.com/gsoultan/panmail/internal/tenant/entities"
)

// recordingFactory remembers the configuration each receiver was built with.
type recordingFactory struct {
	providerEntities.ProviderFactory
	receiver gsmail.Receiver

	mu      sync.Mutex
	configs []string
}

func (f *recordingFactory) CreateReceiver(p *providerEntities.EmailProvider) (gsmail.Receiver, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs = append(f.configs, string(p.Config))
	return f.receiver, nil
}

func (f *recordingFactory) builtWith(config string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.configs {
		if c == config {
			n++
		}
	}
	return n
}

func tenantsA() []*tenantEntities.Tenant {
	return []*tenantEntities.Tenant{{ID: tenantA}}
}

func configured(id, config string) *providerEntities.EmailProvider {
	p := imapProvider(id)
	p.Config = []byte(config)
	return p
}

// A session was keyed by provider id alone, so one opened before an operator
// corrected a password or moved the provider to a new host kept the old
// settings until the process restarted -- and if those settings were why it
// failed, it reconnected with them forever.
func TestASessionIsReopenedWhenItsProviderChanges(t *testing.T) {
	const before, after = `{"host":"old.example.com"}`, `{"host":"new.example.com"}`

	providers := &fakeProviders{}
	providers.set(configured("p1", before))
	// Held open until cancelled, as a healthy IDLE is.
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { select {} }}
	factory := &recordingFactory{receiver: idler}

	poller := NewPoller(&fakeTenants{tenants: tenantsA()}, providers, &recordingUsecase{}, factory, time.Hour)
	s := NewIdleSupervisor(poller)
	s.retryDelay, s.maxDelay, s.refresh = time.Millisecond, 5*time.Millisecond, 5*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the session to open with the first configuration", func() bool {
		return factory.builtWith(before) == 1 && s.Sessions() == 1
	})

	providers.set(configured("p1", after))
	eventually(t, "a session with the new configuration", func() bool { return factory.builtWith(after) >= 1 })

	// Reconcile runs every few milliseconds. The replaced session's exit used
	// to delete the map entry -- by then its replacement's -- so the
	// supervisor lost track of a live connection and opened another, over and
	// over.
	time.Sleep(40 * time.Millisecond)
	if got := factory.builtWith(after); got != 1 {
		t.Errorf("opened %d sessions with the new configuration, want 1", got)
	}
	if got := s.Sessions(); got != 1 {
		t.Errorf("tracking %d sessions for one provider", got)
	}
	if got := factory.builtWith(before); got != 1 {
		t.Errorf("reconnected %d times with the old configuration after it changed", got-1)
	}
}

func TestAnUnchangedProviderKeepsItsSession(t *testing.T) {
	providers := &fakeProviders{}
	providers.set(configured("p1", `{"host":"mail.example.com"}`))
	idler := &fakeIdler{deliver: func(int) ([]gsmail.Email, error) { select {} }}
	factory := &recordingFactory{receiver: idler}

	poller := NewPoller(&fakeTenants{tenants: tenantsA()}, providers, &recordingUsecase{}, factory, time.Hour)
	s := NewIdleSupervisor(poller)
	s.retryDelay, s.maxDelay, s.refresh = time.Millisecond, 5*time.Millisecond, 5*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the session to open", func() bool { return s.Sessions() == 1 })

	// The provider list is re-read every reconcile and each read is a fresh
	// value; an equal configuration must not count as a change.
	providers.set(configured("p1", `{"host":"mail.example.com"}`))
	time.Sleep(40 * time.Millisecond)

	if got := idler.sessionCount(); got != 1 {
		t.Errorf("opened %d sessions for a provider that did not change", got)
	}
}
