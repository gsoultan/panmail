// Package entities holds the SMTP submission listener's configuration as a
// domain type.
//
// The listener used to be built once from process flags and could not change
// while the process lived, which made its configuration a startup concern and
// nothing else. It is now editable by an administrator, and that moves two
// things into this package: the state has to survive a restart, so it is
// stored; and it is set by whoever holds an admin session rather than by
// whoever can edit a systemd unit, so it has to be validated as hostile input.
//
// The validation here is deliberately stricter than the flag path in
// cmd/api/main.go, which only warns. That asymmetry is the point. A flag is set
// by someone with shell access on the host, who could read the keys out of the
// database anyway; a form is submitted by anyone holding an admin cookie. The
// SMTP password is a tenant's API key, so a configuration that puts it on the
// wire in the clear is a credential disclosure that a stolen session must not
// be able to arrange.
package entities

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// BindScope is where the listener accepts connections.
//
// A closed set rather than a host string: the address decides how far the port
// is reachable, and an operator-typed host is unvalidatable input for a
// decision with exactly two useful answers.
type BindScope string

const (
	// BindScopeLoopback binds 127.0.0.1, reachable only from this host.
	BindScopeLoopback BindScope = "loopback"

	// BindScopeAllInterfaces binds 0.0.0.0.
	BindScopeAllInterfaces BindScope = "all_interfaces"
)

// Hosts the scopes bind. Named rather than inline so the string that decides
// reachability appears exactly once.
const (
	loopbackHost      = "127.0.0.1"
	allInterfacesHost = "0.0.0.0"
)

// Ports that mean something specific to a mail client.
const (
	// SubmissionPort is the IANA submission port and the default here.
	SubmissionPort = 587

	// smtpRelayPort is where a mail exchanger answers. Refused: panmail is a
	// submission service that authenticates every sender, not an MX, and a
	// gateway listening on 25 collects delivery attempts it will reject and
	// the operator reputation that comes with them.
	smtpRelayPort = 25

	maxPort = 65535
)

// Validation failures. They are values because the usecase maps them to RPC
// codes, and because a test asserting on message text would pass while the
// meaning drifted underneath it.
var (
	ErrInvalidBindScope = errors.New("smtp submission: bind scope must be loopback or all interfaces")
	ErrInvalidPort      = errors.New("smtp submission: port must be between 1 and 65535")
	ErrRelayPort        = errors.New("smtp submission: port 25 is the mail exchanger port and is not a submission port; use 587")

	// ErrInsecureAuthOffLoopback is the guard this package exists for.
	ErrInsecureAuthOffLoopback = errors.New(
		"smtp submission: AUTH without TLS is only allowed on a loopback listener, because the SMTP password is an API key")

	ErrTLSRequired = errors.New(
		"smtp submission: a listener on every interface requires a TLS certificate")

	ErrIncompleteTLS = errors.New("smtp submission: a certificate and a private key must be supplied together")

	// ErrNoAuthPath is a listener nobody could authenticate to: no certificate
	// to protect the password, and no explicit decision to accept it without
	// one. go-smtp would take the connection and refuse every AUTH, so this is
	// refused at the point where it can still be explained.
	ErrNoAuthPath = errors.New(
		"smtp submission: enabling the listener needs either a TLS certificate or, on loopback, permission to accept AUTH without one")
)

// Config is the desired state of the listener, as stored.
//
// The PEM fields hold key material. TLSKeyPEM is encrypted before it reaches
// the database and is never returned by the API; see Describe, which is the
// only way this type is rendered outward.
type Config struct {
	Enabled   bool
	BindScope BindScope
	Port      int

	TLSCertPEM string
	TLSKeyPEM  string

	AllowInsecureAuth bool

	UpdatedAt time.Time
}

// Default is the configuration a database with no row resolves to: off, and
// shaped so that enabling it is a one-field change.
func Default() *Config {
	return &Config{
		Enabled:   false,
		BindScope: BindScopeLoopback,
		Port:      SubmissionPort,
	}
}

// HasTLS reports whether a keypair is stored.
func (c *Config) HasTLS() bool {
	return c.TLSCertPEM != "" && c.TLSKeyPEM != ""
}

// Host is the address the scope binds.
func (c *Config) Host() string {
	if c.BindScope == BindScopeAllInterfaces {
		return allInterfacesHost
	}
	return loopbackHost
}

// Addr is the listen address, host:port.
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Host(), strconv.Itoa(c.Port))
}

// Validate reports whether this configuration is safe to serve.
//
// It runs on every write, including the one that loads a stored row at startup:
// a row written by an older build, or edited in the database by hand, gets the
// same scrutiny as a form submission. Refusing to serve a configuration that
// would leak credentials is worth more than preserving one that already exists.
//
// The structural rules run whether or not the listener is enabled: storing a
// port or a keypair that could never be served would turn the next toggle into
// a failure with no obvious cause, and the administrator who set the port is
// better placed to fix it than the one who flips the switch three weeks later.
// The one rule held back for Enabled is ErrNoAuthPath, because "no certificate
// and no insecure-auth" is exactly the shape of a listener that is simply off,
// and it is what Default returns.
func (c *Config) Validate() error {
	switch c.BindScope {
	case BindScopeLoopback, BindScopeAllInterfaces:
	default:
		return ErrInvalidBindScope
	}

	if c.Port <= 0 || c.Port > maxPort {
		return ErrInvalidPort
	}
	if c.Port == smtpRelayPort {
		return ErrRelayPort
	}

	if (c.TLSCertPEM == "") != (c.TLSKeyPEM == "") {
		return ErrIncompleteTLS
	}

	// The two rules that make this stricter than the flag path, in the order
	// they matter. Insecure auth off loopback is refused outright rather than
	// warned about, and a public listener without a certificate is refused
	// even when nobody asked for insecure auth — go-smtp would accept the
	// connection and reject every AUTH, which is a listener that cannot be used
	// for the one thing it is for.
	if c.AllowInsecureAuth && c.BindScope != BindScopeLoopback {
		return ErrInsecureAuthOffLoopback
	}
	if c.BindScope == BindScopeAllInterfaces && !c.HasTLS() {
		return ErrTLSRequired
	}

	if c.Enabled && !c.HasTLS() && !c.AllowInsecureAuth {
		return ErrNoAuthPath
	}

	if c.HasTLS() {
		if _, err := c.Keypair(); err != nil {
			return err
		}
	}
	return nil
}

// Keypair parses the stored PEM into a usable certificate.
//
// tls.X509KeyPair is what proves the private key belongs to the certificate.
// Checking them separately would accept a mismatched pair that fails at the
// first handshake instead of at the point where somebody could fix it.
func (c *Config) Keypair() (tls.Certificate, error) {
	cert, err := tls.X509KeyPair([]byte(c.TLSCertPEM), []byte(c.TLSKeyPEM))
	if err != nil {
		// Wrapped rather than returned bare: the caller turns this into an API
		// message, and crypto/tls errors name the problem without quoting the
		// key.
		return tls.Certificate{}, fmt.Errorf("smtp submission: invalid TLS keypair: %w", err)
	}
	return cert, nil
}

// CertificateInfo is what a read may say about the stored keypair. Every field
// is derived from the certificate, which is public; the private key has no
// representation here and none anywhere else that leaves the process.
type CertificateInfo struct {
	Subject           string
	Issuer            string
	DNSNames          []string
	NotBefore         time.Time
	NotAfter          time.Time
	FingerprintSHA256 string
	Expired           bool
}

// Describe summarises the stored certificate, or nil when none is stored.
//
// An unparseable certificate also gives nil rather than an error: this is
// called on the read path to render a panel, and a stored row that cannot be
// described should not stop an administrator from seeing the rest of the
// listener's state — or from replacing the certificate that caused it.
func (c *Config) Describe(now time.Time) *CertificateInfo {
	if c.TLSCertPEM == "" {
		return nil
	}
	keypair, err := c.Keypair()
	if err != nil || len(keypair.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(keypair.Certificate[0])
	if err != nil {
		return nil
	}

	sum := sha256.Sum256(leaf.Raw)
	return &CertificateInfo{
		Subject:           leaf.Subject.String(),
		Issuer:            leaf.Issuer.String(),
		DNSNames:          leaf.DNSNames,
		NotBefore:         leaf.NotBefore,
		NotAfter:          leaf.NotAfter,
		FingerprintSHA256: hex.EncodeToString(sum[:]),
		Expired:           now.After(leaf.NotAfter),
	}
}
