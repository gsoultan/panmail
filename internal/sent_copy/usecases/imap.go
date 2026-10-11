package usecases

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/gsoultan/panmail/internal/sent_copy/entities"
)

// ErrNoSTARTTLS is returned when a server reached without implicit TLS does
// not offer STARTTLS. panmail does not log in over a connection a password
// would cross in the clear.
var ErrNoSTARTTLS = errors.New("sent copies: the IMAP server does not offer STARTTLS, and panmail will not send a password unencrypted")

// imapTimeout bounds every network step of one append.
const imapTimeout = 60 * time.Second

// Appender files a message in a mailbox.
type Appender interface {
	Append(ctx context.Context, set *entities.Settings, login, password string, message []byte, when time.Time) error
}

// IMAPAppender files messages over IMAP, always encrypted: implicit TLS, or
// STARTTLS that the server must offer.
type IMAPAppender struct {
	// RootCAs replaces the system roots when set. Tests use it to trust a
	// server they started.
	RootCAs *x509.CertPool
}

// Append logs in as login and appends message to the settings' Sent folder,
// marked as read, dated when.
func (a *IMAPAppender) Append(ctx context.Context, set *entities.Settings, login, password string, message []byte, when time.Time) error {
	tlsConfig := &tls.Config{
		ServerName: set.IMAPHost,
		MinVersion: tls.VersionTLS12,
		RootCAs:    a.RootCAs,
	}

	deadline := time.Now().Add(imapTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := &net.Dialer{Deadline: deadline}

	var (
		c   *client.Client
		err error
	)
	if set.IMAPUseSSL {
		c, err = client.DialWithDialerTLS(dialer, set.Addr(), tlsConfig)
	} else {
		c, err = client.DialWithDialer(dialer, set.Addr())
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", set.Addr(), err)
	}
	defer func() { _ = c.Logout() }()
	c.Timeout = time.Until(deadline)

	if !set.IMAPUseSSL {
		ok, err := c.SupportStartTLS()
		if err != nil {
			return fmt.Errorf("ask %s for STARTTLS: %w", set.Addr(), err)
		}
		if !ok {
			return ErrNoSTARTTLS
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS with %s: %w", set.Addr(), err)
		}
	}

	if err := c.Login(login, password); err != nil {
		// The login is named, the password never.
		return fmt.Errorf("log in to %s as %s: %w", set.Addr(), login, err)
	}
	if err := c.Append(set.Folder(), []string{imap.SeenFlag}, when, bytes.NewBuffer(message)); err != nil {
		return fmt.Errorf("file the copy in %q: %w", set.Folder(), err)
	}
	return nil
}
