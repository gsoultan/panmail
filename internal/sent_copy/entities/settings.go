// Package entities holds a tenant's Sent copies setting: how a message sent
// through panmail is copied back to its sender, so it still appears in that
// person's Sent folder.
//
// A mail client that sends through panmail files its own copy over IMAP, but
// not every client does, and one that relied on its old mail server to file
// the copy finds nothing there once the mail goes through panmail instead. The
// administrator picks how panmail makes up for that, per tenant.
package entities

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

// Mode is how a copy reaches the sender.
type Mode string

const (
	// ModeNone makes no copy. It is the default, and what a tenant with no
	// stored setting has.
	ModeNone Mode = "none"

	// ModeSharedLogin logs in to each sender's mailbox as one administrator
	// account (an IMAP master user) and files the copy in the Sent folder.
	ModeSharedLogin Mode = "shared_login"

	// ModePerUserLogin logs in to each sender's mailbox with that mailbox's own
	// stored login.
	ModePerUserLogin Mode = "per_user_login"

	// ModeBccSender mails the copy to the sender through the message's own
	// provider. No mailbox login is involved.
	ModeBccSender Mode = "bcc_sender"
)

// Defaults for the IMAP fields an administrator may leave empty.
const (
	DefaultSentFolder      = "Sent"
	DefaultMasterSeparator = "*"
	DefaultPortSSL         = 993
	DefaultPortSTARTTLS    = 143

	maxPort = 65535
)

// Validation failures. Values, so the usecase can map them to RPC codes.
var (
	ErrInvalidMode        = errors.New("sent copies: mode must be none, shared login, per-user login or bcc to sender")
	ErrHostRequired       = errors.New("sent copies: an IMAP server is required for the login modes")
	ErrInvalidHost        = errors.New("sent copies: the IMAP server must be a host name or address, without a scheme or port")
	ErrInvalidPort        = errors.New("sent copies: the IMAP port must be between 1 and 65535")
	ErrMasterRequired     = errors.New("sent copies: shared login needs the administrator's username and password")
	ErrInvalidFolder      = errors.New("sent copies: the Sent folder name may not contain line breaks")
	ErrInvalidSeparator   = errors.New("sent copies: the separator must be a single printable character other than a space")
	ErrAddressRequired    = errors.New("sent copies: a mailbox needs the sender address it belongs to")
	ErrPasswordRequired   = errors.New("sent copies: a new mailbox needs a password")
	ErrInvalidCredentials = errors.New("sent copies: usernames and passwords may not contain line breaks")
)

// Settings is a tenant's stored choice.
type Settings struct {
	TenantID string
	Mode     Mode

	IMAPHost   string
	IMAPPort   int
	IMAPUseSSL bool
	SentFolder string

	MasterUsername  string
	MasterPassword  string
	MasterSeparator string

	UpdatedAt time.Time
}

// Default is what a tenant with no stored row has.
func Default(tenantID string) *Settings {
	return &Settings{TenantID: tenantID, Mode: ModeNone, IMAPUseSSL: true}
}

// UsesIMAP reports whether the mode files copies over IMAP.
func (s *Settings) UsesIMAP() bool {
	return s.Mode == ModeSharedLogin || s.Mode == ModePerUserLogin
}

// Port is the IMAP port to dial, applying the default for the TLS mode.
func (s *Settings) Port() int {
	if s.IMAPPort > 0 {
		return s.IMAPPort
	}
	if s.IMAPUseSSL {
		return DefaultPortSSL
	}
	return DefaultPortSTARTTLS
}

// Addr is host:port.
func (s *Settings) Addr() string {
	return net.JoinHostPort(s.IMAPHost, strconv.Itoa(s.Port()))
}

// Folder is the folder copies are filed in.
func (s *Settings) Folder() string {
	if s.SentFolder != "" {
		return s.SentFolder
	}
	return DefaultSentFolder
}

// Separator joins the sender's login and the master username.
func (s *Settings) Separator() string {
	if s.MasterSeparator != "" {
		return s.MasterSeparator
	}
	return DefaultMasterSeparator
}

// MasterLogin is the login used for one sender in shared login mode, as in
// "alice@example.com*admin".
func (s *Settings) MasterLogin(senderLogin string) string {
	return senderLogin + s.Separator() + s.MasterUsername
}

// Normalise trims what an administrator typed and drops the master password
// when shared login is not selected, so a stored row never carries a secret
// for a mode nobody chose. The server fields are kept, so switching to None
// and back does not mean typing them again.
func (s *Settings) Normalise() {
	if s.Mode == "" {
		s.Mode = ModeNone
	}
	s.IMAPHost = strings.TrimSpace(s.IMAPHost)
	s.SentFolder = strings.TrimSpace(s.SentFolder)
	s.MasterUsername = strings.TrimSpace(s.MasterUsername)
	s.MasterSeparator = strings.TrimSpace(s.MasterSeparator)

	if s.Mode != ModeSharedLogin {
		s.MasterPassword = ""
	}
}

// Validate reports whether the settings can be used. Call Normalise first.
func (s *Settings) Validate() error {
	switch s.Mode {
	case ModeNone, ModeBccSender:
		return nil
	case ModeSharedLogin, ModePerUserLogin:
	default:
		return ErrInvalidMode
	}

	if s.IMAPHost == "" {
		return ErrHostRequired
	}
	if strings.ContainsAny(s.IMAPHost, " /:\r\n\t") && net.ParseIP(s.IMAPHost) == nil {
		return ErrInvalidHost
	}
	if s.IMAPPort < 0 || s.IMAPPort > maxPort {
		return ErrInvalidPort
	}
	if strings.ContainsAny(s.SentFolder, "\r\n") {
		return ErrInvalidFolder
	}

	if s.Mode == ModeSharedLogin {
		if s.MasterUsername == "" || s.MasterPassword == "" {
			return ErrMasterRequired
		}
		if hasLineBreak(s.MasterUsername) || hasLineBreak(s.MasterPassword) {
			return ErrInvalidCredentials
		}
		if sep := s.MasterSeparator; sep != "" && (len(sep) != 1 || sep[0] <= ' ' || sep[0] > '~') {
			return ErrInvalidSeparator
		}
	}
	return nil
}

// Mailbox is one sender's own login, for per-user login mode.
type Mailbox struct {
	TenantID  string
	Address   string
	Username  string
	Password  string
	UpdatedAt time.Time
}

// Login is the IMAP username, the address itself when none was given.
func (m *Mailbox) Login() string {
	if m.Username != "" {
		return m.Username
	}
	return m.Address
}

// NormaliseAddress is the key a mailbox is stored and looked up under.
func NormaliseAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

func hasLineBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

// ValidateMailbox checks a mailbox about to be saved. requirePassword is set
// when no password is stored yet.
func ValidateMailbox(m *Mailbox, requirePassword bool) error {
	if m.Address == "" || !strings.Contains(m.Address, "@") {
		return ErrAddressRequired
	}
	if requirePassword && m.Password == "" {
		return ErrPasswordRequired
	}
	if hasLineBreak(m.Username) || hasLineBreak(m.Password) || hasLineBreak(m.Address) {
		return ErrInvalidCredentials
	}
	return nil
}

// Job is a copy waiting to be filed.
type Job struct {
	ID        string
	TenantID  string
	MessageID string
	// Request is the accepted SendEmailRequest as protojson, what the copy is
	// rendered from.
	Request    []byte
	Attempts   int
	ClaimToken string
	LastError  string
	CreatedAt  time.Time
}
