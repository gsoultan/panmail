package smtp

import (
	gosmtp "github.com/emersion/go-smtp"
)

// backend hands each connection a session. It holds no per-connection state
// of its own, so one instance serves every client.
type backend struct {
	server *Server
}

var _ gosmtp.Backend = (*backend)(nil)

// NewSession starts an unauthenticated session. Nothing but AUTH is accepted
// until it has a tenant.
func (b *backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{server: b.server, conn: c}, nil
}
