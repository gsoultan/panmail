package smtp

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/email/transports/smtp/mime"
)

// session is one SMTP conversation. go-smtp serialises the calls on it, so it
// needs no locking of its own.
type session struct {
	server *Server
	conn   *gosmtp.Conn

	// tenantID and providerID are set by a successful AUTH and cleared by
	// nothing: a session authenticates once and keeps that identity until it
	// closes.
	tenantID   string
	providerID string
	keyID      string

	// from and rcpts are the envelope, reset between messages.
	from  string
	rcpts []string
}

var (
	_ gosmtp.Session     = (*session)(nil)
	_ gosmtp.AuthSession = (*session)(nil)
)

// AuthMechanisms advertises what a client may authenticate with.
//
// PLAIN and LOGIN are both offered because the applications this transport
// exists for are not all modern: LOGIN is what a good deal of older client
// tooling sends, and refusing it would mean refusing them.
func (s *session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

// Auth returns the SASL server for the chosen mechanism.
func (s *session) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			return s.authenticate(username, password)
		}), nil
	case sasl.Login:
		return newLoginServer(s.authenticate), nil
	default:
		return nil, gosmtp.ErrAuthUnknownMechanism
	}
}

// authenticate resolves the credentials into a tenant and a provider.
//
// The username carries the provider id and the password the API key. SMTP has
// no field for "which provider", and this is the only pair of strings every
// client can already send.
func (s *session) authenticate(username, password string) error {
	ctx, cancel := context.WithTimeout(s.server.baseCtx, s.server.authTimeout)
	defer cancel()

	key, err := s.server.verifier.VerifyApiKey(ctx, password)
	if err != nil {
		// The reason is logged but not returned: telling a caller whether a
		// key was unknown, disabled or expired tells an attacker which keys
		// exist.
		s.server.logger.Warn("smtp auth rejected",
			"remote", s.remoteAddr(), "reason", err.Error())
		return permanentError(codeAuthFailed, enhancedBadCredentials, "Authentication failed")
	}

	if !hasScope(key.Scopes, entities.ScopeEmailSend) {
		s.server.logger.Warn("smtp auth rejected: key cannot send",
			"remote", s.remoteAddr(), "key_id", key.ID, "tenant", key.TenantID)
		return permanentError(codeAuthFailed, enhancedBadCredentials,
			"This API key is not permitted to send email")
	}

	// A provider in the username is optional here: a client may instead name
	// one per message with the X-Panmail-Provider-Id header. What is not
	// optional is that anything supplied is well formed, so a typo fails at
	// login rather than on every message.
	provider := strings.TrimSpace(username)
	if provider != "" {
		if err := validateProviderID(provider); err != nil {
			return permanentError(codeAuthFailed, enhancedBadCredentials, "%s", err.Error())
		}
	}

	s.tenantID = key.TenantID
	s.keyID = key.ID
	s.providerID = provider

	s.server.logger.Info("smtp session authenticated",
		"remote", s.remoteAddr(), "tenant", key.TenantID, "key_id", key.ID)
	return nil
}

// Mail records the envelope sender.
func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	if s.tenantID == "" {
		return errNotAuthenticated()
	}
	s.from = from
	s.rcpts = nil
	return nil
}

// Rcpt records an envelope recipient.
func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if s.tenantID == "" {
		return errNotAuthenticated()
	}
	if len(s.rcpts) >= s.server.maxRecipients {
		return permanentError(codeNotAuthorized, enhancedNotAuthorized,
			"Too many recipients, the limit is %d", s.server.maxRecipients)
	}
	s.rcpts = append(s.rcpts, to)
	return nil
}

// Data parses the submission and hands it to the send pipeline.
func (s *session) Data(r io.Reader) error {
	if s.tenantID == "" {
		return errNotAuthenticated()
	}
	if len(s.rcpts) == 0 {
		return permanentError(codeNotAuthorized, enhancedNoValidRecipient,
			"No valid recipients")
	}

	// The envelope sender is passed as the fallback From. A submission with
	// no From header is malformed but recoverable, and the address bounces
	// already go to is the honest sender to use. It stays subject to the
	// provider's AllowedDomains check, so this forgives a missing header
	// without forgiving a spoofed one.
	parsed, err := s.server.parser.Parse(r, s.from)
	if err != nil {
		if errors.Is(err, mime.ErrNoFrom) {
			return permanentError(codeNotAuthorized, enhancedNotAuthorized,
				"Message has no From address and the envelope sender is empty")
		}
		return permanentError(codeBadArguments, enhancedBadArguments,
			"Message could not be parsed: %s", err.Error())
	}

	providerID, err := s.resolveProvider(parsed.ProviderID)
	if err != nil {
		return err
	}

	req := parsed.Request
	req.ProviderId = providerID
	// The envelope decides who actually receives the message, and the
	// difference between it and the visible headers is exactly what a Bcc is.
	// Reconstructing it here keeps blind recipients out of the headers the
	// pipeline builds from To and Cc.
	req.Bcc = blindRecipients(s.rcpts, req.To, req.Cc)

	ctx, cancel := context.WithTimeout(s.server.baseCtx, s.server.sendTimeout)
	defer cancel()

	res, err := s.server.sender.SendEmail(ctx, s.tenantID, req)
	if err != nil {
		s.server.logger.Warn("smtp submission refused",
			"tenant", s.tenantID,
			"key_id", s.keyID,
			"provider", providerID,
			"recipients", len(s.rcpts),
			"retry_after", retryAfterSeconds(err),
			"error", err.Error(),
		)
		return submissionError(err)
	}

	// Only now is the message queued, so only now may the client be told it
	// was accepted. A 250 written any earlier would be a promise the gateway
	// had not yet kept.
	s.server.logger.Info("smtp submission accepted",
		"tenant", s.tenantID,
		"key_id", s.keyID,
		"provider", providerID,
		"message_id", res.GetMessageId(),
		"recipients", len(s.rcpts),
	)
	return nil
}

// resolveProvider decides which provider carries this message. A header on the
// message wins over the one from AUTH, so a single authenticated connection
// can still send through more than one provider.
func (s *session) resolveProvider(fromHeader string) (string, error) {
	provider := strings.TrimSpace(fromHeader)
	if provider == "" {
		provider = s.providerID
	}
	if provider == "" {
		return "", permanentError(codeBadArguments, enhancedBadArguments,
			"No provider given: send the provider id as the AUTH username or in the %s header",
			mime.ProviderHeader)
	}
	if err := validateProviderID(provider); err != nil {
		return "", permanentError(codeBadArguments, enhancedBadArguments, "%s", err.Error())
	}
	return provider, nil
}

// Reset discards the message in flight. The authenticated identity survives:
// RSET resets the transaction, not the session.
func (s *session) Reset() {
	s.from = ""
	s.rcpts = nil
}

// Logout releases the session.
func (s *session) Logout() error {
	s.Reset()
	s.tenantID = ""
	s.providerID = ""
	s.keyID = ""
	return nil
}

func (s *session) remoteAddr() string {
	if s.conn == nil {
		return ""
	}
	if addr := s.conn.Conn(); addr != nil {
		return addr.RemoteAddr().String()
	}
	return ""
}

// blindRecipients returns the envelope addresses that appear in no visible
// header. Those are the Bcc, and the pipeline is trusted to keep them out of
// the headers it builds.
func blindRecipients(envelope, to, cc []string) []string {
	visible := make(map[string]struct{}, len(to)+len(cc))
	for _, list := range [][]string{to, cc} {
		for _, address := range list {
			visible[normaliseAddress(address)] = struct{}{}
		}
	}

	var blind []string
	seen := make(map[string]struct{}, len(envelope))
	for _, address := range envelope {
		key := normaliseAddress(address)
		if key == "" {
			continue
		}
		if _, ok := visible[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		blind = append(blind, address)
	}
	return blind
}

func normaliseAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// validateProviderID rejects a provider id the send usecase would reject
// anyway, so the client is told at the point it can still act on it.
func validateProviderID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return errors.New("provider id must be a UUID")
	}
	if parsed == uuid.Nil {
		return errors.New("provider id must not be the nil UUID")
	}
	return nil
}

func hasScope(scopes []entities.Scope, want entities.Scope) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func errNotAuthenticated() error {
	return permanentError(codeAuthFailed, enhancedBadCredentials, "Authentication required")
}
