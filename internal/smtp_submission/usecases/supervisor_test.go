package usecases

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	authentities "github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
)

// The supervisor never calls either of these in these tests: they exercise the
// listener's lifecycle, not a session. They exist because NewServer refuses to
// build without them, which is itself the behaviour being relied on.
type stubVerifier struct{}

func (stubVerifier) VerifyApiKey(context.Context, string) (*authentities.ApiKey, error) {
	return nil, errors.New("not used")
}

type stubSender struct{}

func (stubSender) SendEmail(context.Context, string, *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	return nil, errors.New("not used")
}

func newTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSupervisor(stubVerifier{}, stubSender{}, logger)
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

// freePort returns a port nothing is listening on.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// loopbackConfig is an enabled listener that needs no certificate, which is
// legitimate only because it binds loopback.
func loopbackConfig(port int) *entities.Config {
	return &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeLoopback,
		Port:              port,
		AllowInsecureAuth: true,
	}
}

// assertAccepting dials the port and fails if nothing answers.
func assertAccepting(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("nothing is listening on port %d: %v", port, err)
	}
	_ = conn.Close()
}

func assertNotAccepting(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("port %d is still accepting connections", port)
	}
}

func TestApplyOpensAndClosesTheListener(t *testing.T) {
	supervisor := newTestSupervisor(t)
	port := freePort(t)

	if err := supervisor.Apply(context.Background(), loopbackConfig(port)); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}
	assertAccepting(t, port)

	off := loopbackConfig(port)
	off.Enabled = false
	if err := supervisor.Apply(context.Background(), off); err != nil {
		t.Fatalf("Apply(disabled) = %v, want nil", err)
	}
	assertNotAccepting(t, port)

	if state := supervisor.State(); state.Config != nil {
		t.Errorf("State().Config = %+v, want nil after disabling", state.Config)
	}
}

// The single most important difference from the flag path. At startup a
// listener that would not bind was fatal, so nobody had to be told twice. A
// toggle in a form cannot take the process down, so the failure has to reach
// the caller instead of a log line.
func TestApplyReportsABindFailureToTheCaller(t *testing.T) {
	port := freePort(t)

	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy the port: %v", err)
	}
	defer occupier.Close()

	supervisor := newTestSupervisor(t)
	err = supervisor.Apply(context.Background(), loopbackConfig(port))
	if err == nil {
		t.Fatal("Apply() = nil, want a bind failure")
	}
	if !strings.Contains(err.Error(), "cannot listen") {
		t.Errorf("Apply() = %v, want an error naming the failed bind", err)
	}
	if state := supervisor.State(); state.LastError == "" {
		t.Error("State().LastError is empty, want the bind failure recorded")
	}
}

// A rejected change must not take submission down for everyone who was already
// using it.
func TestAFailedChangeRestoresThePreviousListener(t *testing.T) {
	supervisor := newTestSupervisor(t)
	working := freePort(t)

	if err := supervisor.Apply(context.Background(), loopbackConfig(working)); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}
	assertAccepting(t, working)

	taken := freePort(t)
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(taken)))
	if err != nil {
		t.Fatalf("occupy the port: %v", err)
	}
	defer occupier.Close()

	if err := supervisor.Apply(context.Background(), loopbackConfig(taken)); err == nil {
		t.Fatal("Apply() = nil, want the move to an occupied port to fail")
	}

	assertAccepting(t, working)
	state := supervisor.State()
	if state.Config == nil || state.Config.Port != working {
		t.Fatalf("State().Config = %+v, want the previous listener on port %d", state.Config, working)
	}
	if state.LastError == "" {
		t.Error("State().LastError is empty, want the rejected change explained")
	}
}

// Re-applying an identical configuration must not rebind, because rebinding
// drops the connections of everyone mid-submission for no reason. The
// reconcile loop calls this every 30 seconds.
func TestApplyingTheSameConfigurationIsANoOp(t *testing.T) {
	supervisor := newTestSupervisor(t)
	port := freePort(t)

	cfg := loopbackConfig(port)
	if err := supervisor.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial the listener: %v", err)
	}
	defer conn.Close()

	for i := 0; i < 3; i++ {
		if err := supervisor.Apply(context.Background(), loopbackConfig(port)); err != nil {
			t.Fatalf("Apply(same) = %v, want nil", err)
		}
	}
	assertAccepting(t, port)
}

func TestApplyRefusesAConfigurationThatWouldLeakCredentials(t *testing.T) {
	supervisor := newTestSupervisor(t)

	cfg := &entities.Config{
		Enabled:           true,
		BindScope:         entities.BindScopeAllInterfaces,
		Port:              freePort(t),
		AllowInsecureAuth: true,
	}
	err := supervisor.Apply(context.Background(), cfg)
	if !errors.Is(err, entities.ErrInsecureAuthOffLoopback) {
		t.Fatalf("Apply() = %v, want ErrInsecureAuthOffLoopback", err)
	}
	if state := supervisor.State(); state.Config != nil {
		t.Error("a refused configuration was applied anyway")
	}
}

// Two administrators saving at once must serialise rather than race to bind the
// same port. Run with -race, this is what proves the mutex covers the state the
// Serve goroutine also writes.
func TestConcurrentAppliesAreSerialised(t *testing.T) {
	supervisor := newTestSupervisor(t)
	ports := []int{freePort(t), freePort(t), freePort(t)}

	var wg sync.WaitGroup
	for _, port := range ports {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(p int) {
				defer wg.Done()
				_ = supervisor.Apply(context.Background(), loopbackConfig(p))
				_ = supervisor.State()
			}(port)
		}
	}
	wg.Wait()

	state := supervisor.State()
	if state.Config == nil {
		t.Fatal("State().Config = nil, want one of the applied listeners")
	}
	assertAccepting(t, state.Config.Port)
}

// A cancelled request must still drain the listener it just closed. Inheriting
// the cancellation would leave the port held and make the next bind fail.
func TestShutdownIsNotCancelledWithTheRequest(t *testing.T) {
	supervisor := newTestSupervisor(t)
	port := freePort(t)

	if err := supervisor.Apply(context.Background(), loopbackConfig(port)); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	off := loopbackConfig(port)
	off.Enabled = false
	if err := supervisor.Apply(cancelled, off); err != nil {
		t.Fatalf("Apply(disabled) = %v, want nil", err)
	}
	assertNotAccepting(t, port)

	// The port is genuinely free: rebinding it is the proof.
	if err := supervisor.Apply(context.Background(), loopbackConfig(port)); err != nil {
		t.Fatalf("rebinding the drained port failed: %v", err)
	}
	assertAccepting(t, port)
}

// Reverting to the configuration that is already serving must clear the error
// from the attempt that was rejected, or the dashboard goes on reporting a
// listener that is running perfectly well.
func TestRevertingAfterAFailedChangeClearsTheError(t *testing.T) {
	supervisor := newTestSupervisor(t)
	working := freePort(t)

	if err := supervisor.Apply(context.Background(), loopbackConfig(working)); err != nil {
		t.Fatalf("Apply() = %v, want nil", err)
	}

	taken := freePort(t)
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(taken)))
	if err != nil {
		t.Fatalf("occupy the port: %v", err)
	}
	defer occupier.Close()

	if err := supervisor.Apply(context.Background(), loopbackConfig(taken)); err == nil {
		t.Fatal("Apply() = nil, want a bind failure")
	}
	if supervisor.State().LastError == "" {
		t.Fatal("LastError is empty, want the rejected change recorded")
	}

	// Back to what is already serving.
	if err := supervisor.Apply(context.Background(), loopbackConfig(working)); err != nil {
		t.Fatalf("Apply(revert) = %v, want nil", err)
	}
	if got := supervisor.State().LastError; got != "" {
		t.Errorf("LastError = %q, want empty once the desired state is serving again", got)
	}
	assertAccepting(t, working)
}
