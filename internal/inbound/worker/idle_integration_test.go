package worker

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerUsecases "github.com/gsoultan/panmail/internal/email_provider/usecases"
	tenantEntities "github.com/gsoultan/panmail/internal/tenant/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

// The supervisor against a real IMAP server.
//
// Everything else here drives it through a fake that returns whatever the test
// asks for, which proves the supervisor's own logic and nothing about IMAP. A
// long-lived IDLE connection is the one thing in this system that cannot be
// judged from unit tests: whether the server actually pushes an unsolicited
// EXISTS, whether the client notices, and what happens when the connection dies
// underneath it are all properties of the protocol and the two implementations,
// not of this code.
//
// Skipped unless a server is configured:
//
//	PANMAIL_TEST_IMAP=127.0.0.1:3143 PANMAIL_TEST_SMTP=127.0.0.1:3025 go test ./internal/inbound/worker/
const (
	envIMAP = "PANMAIL_TEST_IMAP"
	envSMTP = "PANMAIL_TEST_SMTP"
)

func imapEndpoints(t *testing.T) (imapHost string, imapPort int, smtpAddr string) {
	t.Helper()

	imap := os.Getenv(envIMAP)
	smtpAddr = os.Getenv(envSMTP)
	if imap == "" || smtpAddr == "" {
		t.Skipf("set %s and %s to run this against a real IMAP server", envIMAP, envSMTP)
	}

	host, portStr, err := net.SplitHostPort(imap)
	if err != nil {
		t.Fatalf("%s=%q is not host:port: %v", envIMAP, imap, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("%s port: %v", envIMAP, err)
	}
	return host, port, smtpAddr
}

func imapProviderFor(t *testing.T, host string, port int, user string) *providerEntities.EmailProvider {
	t.Helper()

	config, err := protojson.Marshal(&panmailv1.ImapConfig{
		Host: host, Port: int32(port),
		Username: user, Password: "secret",
		UseSsl: false, SkipVerify: true,
	})
	if err != nil {
		t.Fatalf("marshal imap config: %v", err)
	}

	return &providerEntities.EmailProvider{
		ID: provider, TenantID: tenantA, Name: "Real IMAP",
		Type:   panmailv1.ProviderType_PROVIDER_TYPE_IMAP,
		Config: config,
	}
}

// deliver puts a message in the mailbox the way anything else would: over SMTP.
func deliver(t *testing.T, smtpAddr, to, subject, body string) {
	t.Helper()

	msg := fmt.Sprintf("From: sender@example.com\r\nTo: %s\r\nSubject: %s\r\n"+
		"Message-ID: <%s@example.com>\r\n\r\n%s\r\n", to, subject, subject, body)

	if err := smtp.SendMail(smtpAddr, nil, "sender@example.com", []string{to}, []byte(msg)); err != nil {
		t.Fatalf("deliver %q: %v", subject, err)
	}
}

func realSupervisor(t *testing.T, host string, port int, user string) (*IdleSupervisor, *recordingUsecase) {
	t.Helper()

	providers := &fakeProviders{}
	providers.set(imapProviderFor(t, host, port, user))
	usecase := &recordingUsecase{}

	poller := NewPoller(
		&fakeTenants{tenants: []*tenantEntities.Tenant{{ID: tenantA}}},
		providers,
		usecase,
		// The real factory, so this exercises the real gsmail IMAP receiver.
		providerUsecases.NewProviderFactory(),
		time.Hour, // the poll is irrelevant here; IDLE is what is under test
	)

	s := NewIdleSupervisor(poller)
	s.retryDelay = 200 * time.Millisecond
	s.maxDelay = time.Second
	s.refresh = 500 * time.Millisecond
	return s, usecase
}

func TestIdleDeliversAMessageFromARealServer(t *testing.T) {
	host, port, smtpAddr := imapEndpoints(t)
	user := "idle-basic@example.com"

	s, usecase := realSupervisor(t, host, port, user)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	// Wait for the session before delivering, or the message arrives before
	// anything is listening and the poll — disabled here — would be what
	// found it. The settle covers the gap between the goroutine existing and
	// IDLE actually being issued.
	eventually(t, "the IDLE session to open", func() bool { return s.Sessions() == 1 })
	time.Sleep(1500 * time.Millisecond)

	deliver(t, smtpAddr, user, "hello-from-idle", "body")

	eventually(t, "the message to arrive over IDLE", func() bool { return usecase.count() >= 1 })

	usecase.mu.Lock()
	defer usecase.mu.Unlock()
	if !strings.Contains(strings.Join(usecase.seen, ","), "hello-from-idle") {
		t.Errorf("processed %v, want the delivered message", usecase.seen)
	}
}

// The failure the supervisor exists for. A real server drops connections, and
// a hung or closed IDLE is silent — the process stays healthy and the mailbox
// stops being read.
func TestIdleRecoversWhenTheServerGoesAway(t *testing.T) {
	host, port, smtpAddr := imapEndpoints(t)
	user := "idle-recovery@example.com"

	s, usecase := realSupervisor(t, host, port, user)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the first session", func() bool { return s.Sessions() == 1 })
	deliver(t, smtpAddr, user, "before-the-drop", "body")
	eventually(t, "the first message", func() bool { return usecase.count() >= 1 })

	// Cut the session the way a server restart or an idle timeout would, by
	// cancelling it from underneath. The supervisor should notice and rebuild.
	s.mu.Lock()
	for _, cancelSession := range s.running {
		cancelSession()
	}
	s.mu.Unlock()

	eventually(t, "the session to be rebuilt", func() bool { return s.Sessions() == 1 })

	// Sessions() counts a goroutine that exists, not one that has finished
	// connecting, authenticating, selecting the mailbox and issued IDLE. A
	// message delivered inside that window is already present when IDLE
	// starts, and IDLE announces arrivals rather than backlog.
	time.Sleep(1500 * time.Millisecond)

	before := usecase.count()
	deliver(t, smtpAddr, user, "after-the-drop", "body")
	eventually(t, "a message after the reconnect", func() bool { return usecase.count() > before })
}

// Two messages arriving in quick succession must both be seen; a client that
// consumes one EXISTS and stops reading loses the rest.
func TestIdleDeliversSeveralMessages(t *testing.T) {
	host, port, smtpAddr := imapEndpoints(t)
	user := "idle-several@example.com"

	s, usecase := realSupervisor(t, host, port, user)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	eventually(t, "the session", func() bool { return s.Sessions() == 1 })
	// Sessions() counts a goroutine, not an established IDLE. Delivering
	// inside that window puts the message in the mailbox before IDLE starts,
	// and IDLE announces arrivals rather than backlog.
	time.Sleep(1500 * time.Millisecond)

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			deliver(t, smtpAddr, user, fmt.Sprintf("burst-%d", i), "body")
		}(i)
	}
	wg.Wait()

	eventually(t, "all three messages", func() bool { return usecase.count() >= 3 })
}

// The reason the poll runs alongside IDLE, demonstrated rather than asserted.
//
// IDLE announces arrivals. A message that lands while the connection is being
// rebuilt is already in the mailbox when IDLE starts, so it is never announced
// and IDLE alone would never see it. The poll is not redundancy here — it is
// what makes inbound correct across a reconnect, and turning it off to "save
// connections" would lose mail silently.
func TestThePollRecoversWhatIdleMissedDuringAReconnect(t *testing.T) {
	host, port, smtpAddr := imapEndpoints(t)
	user := "idle-gap@example.com"

	providers := &fakeProviders{}
	providers.set(imapProviderFor(t, host, port, user))
	usecase := &recordingUsecase{}

	// A poll fast enough to observe. Production runs it at 30s.
	poller := NewPoller(
		&fakeTenants{tenants: []*tenantEntities.Tenant{{ID: tenantA}}},
		providers, usecase, providerUsecases.NewProviderFactory(),
		500*time.Millisecond,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Deliver with nothing listening at all, which is the same position a
	// message is in when it arrives mid-reconnect.
	deliver(t, smtpAddr, user, "arrived-while-disconnected", "body")

	go poller.Start(ctx)

	eventually(t, "the poll to find the message IDLE never saw", func() bool {
		return usecase.count() >= 1
	})
}
