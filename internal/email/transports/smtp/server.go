package smtp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/gsoultan/panmail/internal/email/transports/smtp/mime"
)

// Timeouts for the work a session does on the caller's behalf. They are
// separate from the connection timeouts because a slow database should not be
// mistaken for a slow client.
const (
	defaultAuthTimeout = 10 * time.Second
	defaultSendTimeout = 30 * time.Second
)

// Server accepts SMTP submissions and feeds them to the send pipeline.
type Server struct {
	inner *gosmtp.Server

	verifier APIKeyVerifier
	sender   Sender
	parser   *mime.Parser
	logger   *slog.Logger

	baseCtx       context.Context
	maxRecipients int
	authTimeout   time.Duration
	sendTimeout   time.Duration
	addr          string
}

// NewServer builds a submission listener.
//
// It returns an error rather than a Server when the configuration would put
// API keys on the wire in the clear, because that is a mistake an operator
// should hear about at startup and not discover in a packet capture.
func NewServer(cfg Config, verifier APIKeyVerifier, sender Sender, logger *slog.Logger) (*Server, error) {
	if verifier == nil {
		return nil, fmt.Errorf("smtp: an API key verifier is required")
	}
	if sender == nil {
		return nil, fmt.Errorf("smtp: a sender is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	srv := &Server{
		verifier:      verifier,
		sender:        sender,
		parser:        mime.NewParser(cfg.MaxMessageBytes, cfg.MaxAttachments),
		logger:        logger,
		baseCtx:       context.Background(),
		maxRecipients: cfg.MaxRecipients,
		authTimeout:   defaultAuthTimeout,
		sendTimeout:   defaultSendTimeout,
		addr:          cfg.Addr,
	}

	inner := gosmtp.NewServer(&backend{server: srv})
	inner.Addr = cfg.Addr
	inner.Domain = cfg.Domain
	inner.TLSConfig = cfg.TLSConfig
	inner.MaxMessageBytes = cfg.MaxMessageBytes
	inner.MaxRecipients = cfg.MaxRecipients
	inner.ReadTimeout = cfg.ReadTimeout
	inner.WriteTimeout = cfg.WriteTimeout
	// go-smtp refuses AUTH on an unencrypted connection unless this is set,
	// which is the guard that keeps an API key off the wire. Config has
	// already established that turning it on was deliberate.
	inner.AllowInsecureAuth = cfg.AllowInsecureAuth
	inner.EnableSMTPUTF8 = true
	inner.ErrorLog = &loggerAdapter{logger: logger}

	srv.inner = inner
	return srv, nil
}

// Addr reports the address the server was configured to listen on.
func (s *Server) Addr() string {
	return s.addr
}

// Serve accepts connections on l until it is closed.
func (s *Server) Serve(l net.Listener) error {
	return s.inner.Serve(l)
}

// ListenAndServe binds the configured address and serves it.
func (s *Server) ListenAndServe() error {
	return s.inner.ListenAndServe()
}

// Shutdown stops accepting connections and waits for the ones in flight,
// giving a message already being read a chance to be queued rather than
// dropped after the client was told to send it.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.inner.Shutdown(ctx)
}

// loggerAdapter routes go-smtp's internal errors into structured logging.
type loggerAdapter struct {
	logger *slog.Logger
}

func (a *loggerAdapter) Printf(format string, v ...any) {
	a.logger.Error("smtp server", "message", fmt.Sprintf(format, v...))
}

func (a *loggerAdapter) Println(v ...any) {
	a.logger.Error("smtp server", "message", fmt.Sprint(v...))
}
