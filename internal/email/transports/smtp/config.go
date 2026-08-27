package smtp

import (
	"crypto/tls"
	"errors"
	"time"
)

// Defaults for a submission listener. They are deliberately conservative:
// this port accepts mail from applications on the operator's own network, not
// from the public internet, so the limits describe an application's largest
// reasonable message rather than a mail exchanger's.
const (
	DefaultAddr            = ":587"
	DefaultDomain          = "panmail"
	DefaultMaxMessageBytes = int64(25 << 20)
	DefaultMaxRecipients   = 100
	DefaultMaxAttachments  = 25
	DefaultReadTimeout     = 60 * time.Second
	DefaultWriteTimeout    = 60 * time.Second
	DefaultShutdownTimeout = 15 * time.Second
)

// ErrInsecureAuthWithoutTLS is returned when a Config would put API keys on
// the wire in the clear.
var ErrInsecureAuthWithoutTLS = errors.New(
	"smtp: AUTH without TLS is only allowed when AllowInsecureAuth is set explicitly",
)

// Config describes a submission listener.
type Config struct {
	// Addr is the listen address, host:port.
	Addr string

	// Domain is the hostname the server greets with.
	Domain string

	// TLSConfig enables STARTTLS. Without it, no client may authenticate
	// unless AllowInsecureAuth says so.
	TLSConfig *tls.Config

	// AllowInsecureAuth permits AUTH over an unencrypted connection. It is a
	// deployment decision, not a default: an API key sent in the clear is a
	// tenant's whole sending authority, readable by anything on the path.
	// Set it only where the hop is already private — a container network, or
	// loopback.
	AllowInsecureAuth bool

	// MaxMessageBytes bounds a single submission.
	MaxMessageBytes int64

	// MaxRecipients bounds RCPT TO per message.
	MaxRecipients int

	// MaxAttachments bounds how many parts one message may carry.
	MaxAttachments int

	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// withDefaults fills in the unset fields and reports whether the result is
// safe to serve.
func (c Config) withDefaults() (Config, error) {
	if c.Addr == "" {
		c.Addr = DefaultAddr
	}
	if c.Domain == "" {
		c.Domain = DefaultDomain
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if c.MaxRecipients <= 0 {
		c.MaxRecipients = DefaultMaxRecipients
	}
	if c.MaxAttachments <= 0 {
		c.MaxAttachments = DefaultMaxAttachments
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = DefaultReadTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}

	if c.TLSConfig == nil && !c.AllowInsecureAuth {
		return c, ErrInsecureAuthWithoutTLS
	}
	return c, nil
}
