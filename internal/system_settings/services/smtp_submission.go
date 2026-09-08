package services

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
	smtpentities "github.com/gsoultan/panmail/internal/smtp_submission/entities"
	smtpstore "github.com/gsoultan/panmail/internal/smtp_submission/repositories/stores/postgres"
	smtpusecases "github.com/gsoultan/panmail/internal/smtp_submission/usecases"
)

// UpdateSmtpSubmission opens or closes the submission listener.
//
// Admin-only, enforced by the RBAC interceptor from the procedure table in
// internal/auth/middlewares/policy.go, which is the only place authorisation is
// decided in this codebase. Nothing here re-checks the role, because a handler
// that enforced its own would be the one place a policy change did not reach.
func (s *settingsService) UpdateSmtpSubmission(
	ctx context.Context,
	req *connect.Request[panmailv1.UpdateSmtpSubmissionRequest],
) (*connect.Response[panmailv1.UpdateSmtpSubmissionResponse], error) {
	if s.smtpSubmission == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			errors.New("smtp submission is not available on this gateway"))
	}

	cfg := req.Msg.GetConfig()
	if cfg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a configuration is required"))
	}

	change := smtpusecases.Change{
		Enabled:           cfg.GetEnabled(),
		BindScope:         bindScopeFromProto(cfg.GetBindScope()),
		Port:              int(cfg.GetPort()),
		TLSCertPEM:        cfg.GetTlsCertificatePem(),
		TLSKeyPEM:         cfg.GetTlsPrivateKeyPem(),
		AllowInsecureAuth: cfg.GetAllowInsecureAuth(),
		ClearTLS:          cfg.GetClearTls(),
	}

	snapshot, err := s.smtpSubmission.Update(ctx, change)
	if err != nil {
		return nil, submissionError(err)
	}

	logSubmissionChange(ctx, change, snapshot)

	return connect.NewResponse(&panmailv1.UpdateSmtpSubmissionResponse{
		SmtpSubmission: submissionToProto(snapshot),
	}), nil
}

// describeSubmission renders the listener for GetSettings.
//
// A nil usecase gives a disabled listener rather than an error: submission is
// an optional door, and a gateway built without it should still serve its
// settings page.
func (s *settingsService) describeSubmission(ctx context.Context) (*panmailv1.SmtpSubmission, error) {
	if s.smtpSubmission == nil {
		return &panmailv1.SmtpSubmission{Enabled: false}, nil
	}

	snapshot, err := s.smtpSubmission.Describe(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return submissionToProto(snapshot), nil
}

// submissionError maps a domain failure to the code a client should act on.
//
// Validation failures are InvalidArgument because the caller can fix them by
// sending something else. The two FailedPrecondition cases cannot be fixed by
// changing the request at all — they need a flag removed or a key configured —
// and telling a form to retry with different input would be wrong.
func submissionError(err error) error {
	switch {
	case errors.Is(err, smtpusecases.ErrManagedByFlags),
		errors.Is(err, smtpstore.ErrNoEncryptionKey):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	case errors.Is(err, smtpusecases.ErrTLSPairRequired),
		errors.Is(err, smtpusecases.ErrClearAndSetTLS),
		errors.Is(err, smtpentities.ErrInvalidBindScope),
		errors.Is(err, smtpentities.ErrInvalidPort),
		errors.Is(err, smtpentities.ErrRelayPort),
		errors.Is(err, smtpentities.ErrInsecureAuthOffLoopback),
		errors.Is(err, smtpentities.ErrTLSRequired),
		errors.Is(err, smtpentities.ErrIncompleteTLS),
		errors.Is(err, smtpentities.ErrNoAuthPath):
		return connect.NewError(connect.CodeInvalidArgument, err)

	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// logSubmissionChange records who opened or closed the listener.
//
// This is the audit trail for a change that alters what the gateway exposes to
// the network, so it names the actor the same way a retention change does. The
// certificate is identified by fingerprint: enough to tell which keypair is in
// force, and nothing that would put key material in a log.
func logSubmissionChange(ctx context.Context, change smtpusecases.Change, snapshot *smtpusecases.Snapshot) {
	userID, _ := middlewares.GetUserID(ctx)
	if userID == "" {
		userID = "(api key)"
	}

	fingerprint := ""
	if snapshot.Certificate != nil {
		fingerprint = snapshot.Certificate.FingerprintSHA256
	}

	slog.Warn("SMTP submission listener changed",
		"actor", userID,
		"role", middlewares.GetRole(ctx),
		"tenant_id", middlewares.GetTenantID(ctx),
		"enabled", snapshot.Enabled,
		"bind_scope", string(snapshot.BindScope),
		"port", snapshot.Port,
		"starttls", snapshot.STARTTLS,
		"insecure_auth", snapshot.InsecureAuthAllowed,
		"tls_replaced", change.TLSCertPEM != "",
		"tls_cleared", change.ClearTLS,
		"certificate_sha256", fingerprint,
		"last_error", snapshot.LastError)
}

func bindScopeFromProto(scope panmailv1.SmtpBindScope) smtpentities.BindScope {
	switch scope {
	case panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_ALL_INTERFACES:
		return smtpentities.BindScopeAllInterfaces
	case panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_LOOPBACK:
		return smtpentities.BindScopeLoopback
	default:
		// Unspecified resolves to loopback rather than being rejected: it is
		// the value a client that has not chosen sends, and defaulting to the
		// scope that cannot be reached from another host is the safe reading.
		return smtpentities.BindScopeLoopback
	}
}

func bindScopeToProto(scope smtpentities.BindScope) panmailv1.SmtpBindScope {
	if scope == smtpentities.BindScopeAllInterfaces {
		return panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_ALL_INTERFACES
	}
	return panmailv1.SmtpBindScope_SMTP_BIND_SCOPE_LOOPBACK
}

// submissionToProto renders a snapshot on the wire.
//
// There is no branch here that can emit key material, because Snapshot has no
// field that holds any: the certificate arrives already reduced to metadata by
// the domain. That is the property worth preserving if this ever grows.
func submissionToProto(snapshot *smtpusecases.Snapshot) *panmailv1.SmtpSubmission {
	if snapshot == nil {
		return &panmailv1.SmtpSubmission{Enabled: false}
	}

	out := &panmailv1.SmtpSubmission{
		Enabled:             snapshot.Enabled,
		Host:                snapshot.Host,
		Port:                int32(snapshot.Port),
		Starttls:            snapshot.STARTTLS,
		InsecureAuthAllowed: snapshot.InsecureAuthAllowed,
		ManagedByFlags:      snapshot.ManagedByFlags,
		Editable:            snapshot.Editable,
		NotEditableReason:   snapshot.NotEditableReason,
		BindScope:           bindScopeToProto(snapshot.BindScope),
		LastError:           snapshot.LastError,
	}

	if cert := snapshot.Certificate; cert != nil {
		out.Certificate = &panmailv1.TlsCertificateInfo{
			Subject:           cert.Subject,
			Issuer:            cert.Issuer,
			DnsNames:          cert.DNSNames,
			NotBefore:         cert.NotBefore.UTC().Format(time.RFC3339),
			NotAfter:          cert.NotAfter.UTC().Format(time.RFC3339),
			FingerprintSha256: cert.FingerprintSHA256,
			Expired:           cert.Expired,
		}
	}
	return out
}
