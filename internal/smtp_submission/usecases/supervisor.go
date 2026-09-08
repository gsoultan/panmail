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

	previous := s.running
	s.stopLocked(ctx)

	if !desired.Enabled {
		s.lastErr = ""
		return nil
	}

	if err := s.startLocked(desired); err == nil {
		s.lastErr = ""
		return nil
	} else {
		s.lastErr = err.Error()
		return s.restoreLocked(previous, err)
	}
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
		return fmt.Errorf("%w (the previous listener could not be restored either: %v)", cause, err)
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
	server, err := s.buildLocked(cfg)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", cfg.Addr(), err)
	}

	s.generation++
	generation := s.generation
	s.server = server
	clone := *cfg
	s.running = &clone

	go s.serve(server, listener, generation, cfg.Addr())

	s.logger.Info("SMTP submission listener started",
		"addr", cfg.Addr(),
		"starttls", cfg.HasTLS(),
		"insecure_auth", cfg.AllowInsecureAuth)
	return nil
}

// serve runs the accept loop and records why it ended.
//
// net.ErrClosed is the ordinary stop: Shutdown closes the listener, and the
// accept loop returning is how it reports that it noticed. Anything else is a
// listener that died on its own, which is worth surfacing — the reconcile loop
// will bring it back, and the recorded error explains the gap.
func (s *Supervisor) serve(server *emailsmtp.Server, listener net.Listener, generation uint64, addr string) {
	err := server.Serve(listener)
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

	// Detached from the caller's context on purpose. A request that is
	// cancelled mid-save must still drain the listener it just closed;
	// inheriting the cancellation would leave the port held by sessions nobody
	// is waiting for and make the next bind fail.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
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
