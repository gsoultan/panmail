// Package usecases owns the SMTP submission listener's lifecycle and the rules
// for changing it.
package usecases

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	emailsmtp "github.com/gsoultan/panmail/internal/email/transports/smtp"
	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
)

// shutdownTimeout bounds the wait for in-flight submissions when the listener
// is closed. A session that has already been told to send its message gets the
// chance to finish and be queued; DATA on a large message over a slow link is
// the case this is sized for.
const shutdownTimeout = 15 * time.Second

// Supervisor owns the running listener and is the only thing that starts or
// stops it.
//
// It is safe for concurrent use. Every method takes the same mutex, so two
// administrators saving at once serialise rather than racing to bind the same
// port, and the reconcile loop cannot start a listener that a request is in the
// middle of stopping.
type Supervisor struct {
	mu      sync.Mutex
	server  *emailsmtp.Server
	running *entities.Config

	// listener is held because closing it is this type's job, not the
	// transport's. go-smtp registers a listener inside Serve, and Serve runs on
	// a goroutine, so a Shutdown that arrives first finds an empty list and
	// closes nothing — leaving the accept loop serving a port the supervisor
	// believes it stopped. Enabling and immediately disabling is enough to hit
	// it, and the port then stays open for the life of the process.
	listener net.Listener

	// serveDone is closed once the accept loop has returned.
	//
	// Stopping waits on it before calling Shutdown, because go-smtp mutates its
	// listener slice from both Serve and Shutdown without synchronising the
	// two: overlapping them is a data race in the library, which the race
	// detector catches under concurrent applies. Waiting for the accept loop to
	// exit first means the two never run together.
	serveDone chan struct{}

	// generation identifies a run, so an error surfacing from a Serve
	// goroutine that has already been replaced is discarded instead of
	// overwriting the state of the listener that succeeded it.
	generation uint64
	lastErr    string

	verifier emailsmtp.APIKeyVerifier
	sender   emailsmtp.Sender
	logger   *slog.Logger
}

// NewSupervisor builds a supervisor with nothing running.
func NewSupervisor(
	verifier emailsmtp.APIKeyVerifier,
	sender emailsmtp.Sender,
	logger *slog.Logger,
) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{verifier: verifier, sender: sender, logger: logger}
}

// State is a snapshot of what the supervisor is actually doing.
type State struct {
	// Config is the configuration being served, or nil when nothing is.
	Config *entities.Config

	// LastError is why the listener is not running despite having been asked
	// to run. Empty when desired and actual agree.
	LastError string
}

// State returns what is running right now.
//
// The returned Config is a copy: handing out the pointer the supervisor serves
// from would let a caller edit a running listener's configuration in place,
// where nothing would re-validate it.
func (s *Supervisor) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := State{LastError: s.lastErr}
	if s.running != nil {
		clone := *s.running
		out.Config = &clone
	}
	return out
}

// Apply reconciles the running listener with the desired configuration.
//
// It is idempotent: applying the configuration that is already being served
// does nothing, which is what makes it safe to call from a reconcile loop on a
// timer as well as from a request.
//
// A bind failure is returned rather than logged. This is the difference between
// the flag path and this one: at startup a listener that would not bind was
// fatal, so nobody had to be told twice, but a toggle in a form cannot take the
// process down and an administrator who is not told has no way to find out
// except by trying to send mail.
func (s *Supervisor) Apply(ctx context.Context, desired *entities.Config) error {
	if desired == nil {
		desired = entities.Default()
	}
	if err := desired.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.matchesRunningLocked(desired) {
		// Clearing the error matters as much as skipping the work. Reverting a
		// change that failed to bind leaves the desired state equal to what is
		// already serving, and a lastErr left over from the rejected attempt
		// would keep the dashboard reporting a listener that is running fine.
		s.lastErr = ""
		return nil
	}

	if !desired.Enabled {
		s.stopLocked(ctx)
		s.lastErr = ""
		return nil
	}

	// Moving to a different address binds the new one first, and only gives up
	// the old one once that has succeeded.
	//
	// The order is what keeps a working listener working. Reconcile retries
	// every 30 seconds, so stopping first would tear down and rebuild a healthy
	// listener on every attempt for as long as the desired port stayed
	// unavailable — dropping live sessions each time, on a schedule, because of
	// a port that was never reachable. Observed doing exactly that before this
	// changed.
	if s.running == nil || s.running.Addr() != desired.Addr() {
		server, listener, err := s.bindLocked(desired)
		if err != nil {
			s.lastErr = err.Error()
			return err
		}
		s.stopLocked(ctx)
		s.installLocked(desired, server, listener)
		s.lastErr = ""
		return nil
	}

	// Same address, so the port cannot be held twice: the old listener has to
	// go before the new one can bind, and a failure needs the restore path.
	previous := s.running
	s.stopLocked(ctx)

	if err := s.startLocked(desired); err != nil {
		s.lastErr = err.Error()
		return s.restoreLocked(previous, err)
	}
	s.lastErr = ""
	return nil
}

// restoreLocked puts the previous listener back after a failed change, so a bad
// port does not silently take submission down for everyone.
//
// The restore failing is reported alongside the original cause rather than
// instead of it: the administrator needs to know both that their change was
// rejected and that the listener they had is gone.
func (s *Supervisor) restoreLocked(previous *entities.Config, cause error) error {
	if previous == nil || !previous.Enabled {
		return cause
	}

	if err := s.startLocked(previous); err != nil {
		s.logger.Error("SMTP submission listener could not be restored after a failed change",
			"addr", previous.Addr(), "cause", cause, "error", err)
		combined := fmt.Errorf("%w (the previous listener could not be restored either: %v)", cause, err)
		// The more urgent fact of the two is that nothing is listening now, so
		// that is what the dashboard should read rather than the rejected
		// change alone.
		s.lastErr = combined.Error()
		return combined
	}

	s.lastErr = cause.Error()
	s.logger.Warn("SMTP submission change rejected; the previous listener was restored",
		"addr", previous.Addr(), "error", cause)
	return cause
}

// matchesRunningLocked reports whether the desired state is already being
// served.
//
// Every field compared is one the listener was built from. Comparing
// entities.Config wholesale would include UpdatedAt and rebind the port on
// every save that changed nothing, dropping live connections for no reason.
func (s *Supervisor) matchesRunningLocked(desired *entities.Config) bool {
	if !desired.Enabled {
		return s.running == nil
	}
	if s.running == nil {
		return false
	}
	return s.running.Addr() == desired.Addr() &&
		s.running.AllowInsecureAuth == desired.AllowInsecureAuth &&
		s.running.TLSCertPEM == desired.TLSCertPEM &&
		s.running.TLSKeyPEM == desired.TLSKeyPEM
}

// startLocked binds and serves the configuration, or returns why it could not.
//
// The bind is explicit rather than left to ListenAndServe so that "address
// already in use" reaches the caller. Handing the address to a goroutine would
// put that error in a log line and return success to an administrator whose
// listener is not running.
func (s *Supervisor) startLocked(cfg *entities.Config) error {
	server, listener, err := s.bindLocked(cfg)
	if err != nil {
		return err
	}
	s.installLocked(cfg, server, listener)
	return nil
}

// bindLocked builds the server and takes the port, without touching any state.
//
// Separate from installLocked so a caller can find out whether the new address
// is available while the old listener is still serving the current one.
func (s *Supervisor) bindLocked(cfg *entities.Config) (*emailsmtp.Server, net.Listener, error) {
	server, err := s.buildLocked(cfg)
	if err != nil {
		return nil, nil, err
	}

	listener, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return nil, nil, fmt.Errorf("cannot listen on %s: %w", cfg.Addr(), err)
	}
	return server, listener, nil
}

// installLocked adopts an already-bound listener and starts serving it.
func (s *Supervisor) installLocked(cfg *entities.Config, server *emailsmtp.Server, listener net.Listener) {
	s.generation++
	generation := s.generation
	done := make(chan struct{})
	s.server = server
	s.listener = listener
	s.serveDone = done
	clone := *cfg
	s.running = &clone

	go s.serve(server, listener, generation, cfg.Addr(), done)

	s.logger.Info("SMTP submission listener started",
		"addr", cfg.Addr(),
		"starttls", cfg.HasTLS(),
		"insecure_auth", cfg.AllowInsecureAuth)
}

// serve runs the accept loop and records why it ended.
//
// net.ErrClosed is the ordinary stop: Shutdown closes the listener, and the
// accept loop returning is how it reports that it noticed. Anything else is a
// listener that died on its own, which is worth surfacing — the reconcile loop
// will bring it back, and the recorded error explains the gap.
func (s *Supervisor) serve(
	server *emailsmtp.Server,
	listener net.Listener,
	generation uint64,
	addr string,
	done chan struct{},
) {
	err := server.Serve(listener)

	// Signalled before any lock is taken. A stop holds the mutex while it waits
	// here, so acquiring it first and closing after would deadlock the two
	// against each other.
	close(done)

	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}

	s.logger.Error("SMTP submission listener stopped", "addr", addr, "error", err)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		// A newer listener has taken over. This error belongs to a run that is
		// already gone, and reporting it would describe the wrong listener.
		return
	}
	s.lastErr = err.Error()
	s.server = nil
	s.listener = nil
	s.running = nil
}

// buildLocked turns stored configuration into a transport server.
func (s *Supervisor) buildLocked(cfg *entities.Config) (*emailsmtp.Server, error) {
	transportCfg := emailsmtp.Config{
		Addr:              cfg.Addr(),
		AllowInsecureAuth: cfg.AllowInsecureAuth,
	}

	if cfg.HasTLS() {
		keypair, err := cfg.Keypair()
		if err != nil {
			return nil, err
		}
		transportCfg.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{keypair},
			MinVersion:   tls.VersionTLS12,
		}
	}

	return emailsmtp.NewServer(transportCfg, s.verifier, s.sender, s.logger)
}

// stopLocked shuts the running listener down and waits for it, so the port is
// free before anything tries to bind it again.
func (s *Supervisor) stopLocked(ctx context.Context) {
	if s.server == nil {
		return
	}

	addr := ""
	if s.running != nil {
		addr = s.running.Addr()
	}

	// Closed here rather than left to Shutdown, which cannot be relied on to do
	// it: the transport only learns about this listener once its Serve
	// goroutine has been scheduled. Closing first also stops new connections
	// immediately, which is what Shutdown would do anyway, and leaves it the
	// job it is actually needed for — draining the sessions already in flight.
	if s.listener != nil {
		// Already closed is the ordinary case when the accept loop noticed
		// first, and is not worth reporting.
		_ = s.listener.Close()
		s.listener = nil
	}

	// Then wait for the accept loop to notice, so Serve and Shutdown never
	// touch go-smtp's listener slice at the same time. Bounded rather than
	// unconditional: a wait that could not time out would turn a stuck accept
	// loop into a stuck gateway, and draining is still worth attempting.
	if s.serveDone != nil {
		timer := time.NewTimer(shutdownTimeout)
		select {
		case <-s.serveDone:
		case <-timer.C:
			s.logger.Warn("SMTP submission accept loop did not exit before the deadline",
				"addr", addr)
		}
		timer.Stop()
		s.serveDone = nil
	}

	// Detached from the caller's context on purpose. A request that is
	// cancelled mid-save must still drain the sessions on the listener it just
	// closed; inheriting the cancellation would leave the port held by sessions
	// nobody is waiting for and make the next bind fail.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, net.ErrClosed) {
		s.logger.Warn("SMTP submission listener shutdown did not finish cleanly",
			"addr", addr, "error", err)
	} else {
		s.logger.Info("SMTP submission listener stopped", "addr", addr)
	}

	s.generation++
	s.server = nil
	s.running = nil
}

// Close stops the listener for process shutdown.
func (s *Supervisor) Close(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked(ctx)
}
