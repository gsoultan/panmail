package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gsoultan/panmail/internal/smtp_submission/entities"
	"github.com/gsoultan/panmail/internal/smtp_submission/repositories"
)

// DefaultReconcileInterval is how often an instance re-reads the stored
// configuration when nothing else is asked for.
//
// The settings this sits beside are shared by every gateway against one
// database, and a change saved through one instance has to reach the others or
// the deployment is inconsistent in exactly the way moving settings out of
// config.yaml was meant to end. The same pass restarts a listener that died on
// its own, so the interval is also the worst-case gap in that recovery.
//
// It is a default rather than a constant because the two costs it balances
// scale differently: the settings read is per gateway per interval, so a large
// fleet pays for a short one, while the propagation delay it buys is the same
// whether there are two gateways or fifty. An operator with many instances is
// the one who needs to move it, and they cannot rebuild the binary.
const DefaultReconcileInterval = 30 * time.Second

// MinReconcileInterval is the shortest interval that will be honoured.
//
// Below this the pass stops being a safety net and becomes a load generator
// against the settings row, and the failure it produces — a database busy
// serving reconciles — looks nothing like its cause. A value under this is
// raised to it and said out loud rather than accepted quietly.
const MinReconcileInterval = time.Second

// Errors the service maps to RPC codes.
var (
	ErrManagedByFlags = errors.New(
		"smtp submission: this gateway was started with --smtp-addr, so the listener is configured by process flags and cannot be changed here")

	ErrTLSPairRequired = errors.New(
		"smtp submission: send both the certificate and the private key, or neither to keep the stored pair")

	ErrClearAndSetTLS = errors.New(
		"smtp submission: clearing the stored certificate and supplying a new one are opposite instructions; send one or the other")
)

// FlagListener describes a listener that process flags configured.
//
// It exists so the read path can describe both modes without the caller having
// to know which is in force. Its fields are already resolved — the host is
// empty for a wildcard bind, as the dashboard expects — because flags cannot
// change while the process lives and there is nothing left to decide.
type FlagListener struct {
	Host                string
	Port                int
	STARTTLS            bool
	InsecureAuthAllowed bool
}

// Snapshot is the listener as the dashboard should render it.
//
// It carries no key material. The certificate is described, never disclosed;
// there is no field here that could hold a private key, which is what makes it
// safe to hand the whole struct to the transport layer.
type Snapshot struct {
	// Enabled is the administrator's setting, not proof of a live socket.
	// LastError is what says whether it took effect.
	Enabled bool

	// Host is empty for a wildcard bind. 0.0.0.0 names nothing a client can
	// dial, so the dashboard falls back to base_url and says that it did.
	Host string
	Port int

	STARTTLS            bool
	InsecureAuthAllowed bool
	BindScope           entities.BindScope

	ManagedByFlags    bool
	Editable          bool
	NotEditableReason string

	Certificate *entities.CertificateInfo
	LastError   string
}

// Change is a requested edit.
//
// The TLS fields are write-only and their emptiness is meaningful: absent means
// keep what is stored, which is what lets an administrator change the port
// without re-pasting a private key.
type Change struct {
	Enabled           bool
	BindScope         entities.BindScope
	Port              int
	TLSCertPEM        string
	TLSKeyPEM         string
	AllowInsecureAuth bool
	ClearTLS          bool
}

// Usecase reads and changes the submission listener.
type Usecase interface {
	// Describe renders the listener for the dashboard.
	Describe(ctx context.Context) (*Snapshot, error)

	// Update validates, stores and applies a change.
	Update(ctx context.Context, change Change) (*Snapshot, error)

	// Reconcile brings this instance's listener in line with what is stored.
	Reconcile(ctx context.Context) error

	// Run reconciles on a timer until ctx is done.
	Run(ctx context.Context)
}

type usecase struct {
	repo       repositories.ConfigRepository
	supervisor *Supervisor
	logger     *slog.Logger

	// flags describes a flag-configured listener, or nil when the stored
	// configuration is in force. Flags win: a deployment that passed
	// --smtp-addr keeps behaving exactly as it did, and the dashboard renders
	// read-only rather than offering an edit that would be ignored.
	flags *FlagListener

	// secretsConfigured reports whether a data encryption key exists. Without
	// one a certificate cannot be stored, so the panel says so up front
	// instead of failing at save.
	secretsConfigured bool

	reconcileInterval time.Duration
}

// NewUsecase builds the usecase.
//
// A nil supervisor means this process does not run the listener itself, which
// is the flag-managed case: the flag path in cmd/api owns the socket and this
// only reports it.
//
// A zero reconcileInterval takes DefaultReconcileInterval, so a caller that has
// no opinion does not have to have one.
func NewUsecase(
	repo repositories.ConfigRepository,
	supervisor *Supervisor,
	flags *FlagListener,
	secretsConfigured bool,
	reconcileInterval time.Duration,
	logger *slog.Logger,
) Usecase {
	if logger == nil {
		logger = slog.Default()
	}

	switch {
	case reconcileInterval <= 0:
		reconcileInterval = DefaultReconcileInterval
	case reconcileInterval < MinReconcileInterval:
		logger.Warn("SMTP submission reconcile interval raised to the minimum",
			"requested", reconcileInterval, "using", MinReconcileInterval)
		reconcileInterval = MinReconcileInterval
	}

	return &usecase{
		repo:              repo,
		supervisor:        supervisor,
		flags:             flags,
		secretsConfigured: secretsConfigured,
		reconcileInterval: reconcileInterval,
		logger:            logger,
	}
}

// stored returns the configuration on record, or the default when none is.
func (u *usecase) stored(ctx context.Context) (*entities.Config, error) {
	cfg, err := u.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return entities.Default(), nil
	}
	return cfg, nil
}

func (u *usecase) Describe(ctx context.Context) (*Snapshot, error) {
	if u.flags != nil {
		return &Snapshot{
			Enabled:             true,
			Host:                u.flags.Host,
			Port:                u.flags.Port,
			STARTTLS:            u.flags.STARTTLS,
			InsecureAuthAllowed: u.flags.InsecureAuthAllowed,
			ManagedByFlags:      true,
			Editable:            false,
			NotEditableReason:   ErrManagedByFlags.Error(),
		}, nil
	}

	cfg, err := u.stored(ctx)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{
		Enabled:             cfg.Enabled,
		Port:                cfg.Port,
		STARTTLS:            cfg.HasTLS(),
		InsecureAuthAllowed: cfg.AllowInsecureAuth,
		BindScope:           cfg.BindScope,
		Certificate:         cfg.Describe(time.Now()),
		Editable:            u.secretsConfigured,
	}
	if !u.secretsConfigured {
		snapshot.NotEditableReason = "no data encryption key is configured, so a TLS private key could not be stored safely"
	}

	// A wildcard bind names no host a client could dial. Reporting it would
	// send an integrator to 0.0.0.0; reporting nothing lets the dashboard fall
	// back to the host in base_url and say what it did.
	if cfg.BindScope == entities.BindScopeLoopback {
		snapshot.Host = cfg.Host()
	}

	if u.supervisor != nil {
		snapshot.LastError = u.supervisor.State().LastError
	}
	return snapshot, nil
}

func (u *usecase) Update(ctx context.Context, change Change) (*Snapshot, error) {
	if u.flags != nil {
		return nil, ErrManagedByFlags
	}

	current, err := u.stored(ctx)
	if err != nil {
		return nil, err
	}

	desired, err := merge(current, change)
	if err != nil {
		return nil, err
	}
	if err := desired.Validate(); err != nil {
		return nil, err
	}

	// Store before applying. An applied-but-unstored listener would vanish on
	// the next restart with nothing to explain why, and a stored-but-unapplied
	// one reports last_error and is retried by the reconcile loop — so of the
	// two half-states, this is the recoverable one.
	desired.UpdatedAt = time.Now().UTC()
	if err := u.repo.Save(ctx, desired); err != nil {
		return nil, err
	}

	var applyErr error
	if u.supervisor != nil {
		applyErr = u.supervisor.Apply(ctx, desired)
	}

	snapshot, err := u.Describe(ctx)
	if err != nil {
		return nil, err
	}
	if applyErr != nil {
		// Reported in the snapshot rather than returned as a failure: the
		// change was accepted and stored, and the caller needs to see that
		// alongside the reason it is not serving.
		snapshot.LastError = applyErr.Error()
	}
	return snapshot, nil
}

// merge folds a change into the stored configuration.
//
// The TLS rules are the reason this is a function rather than a struct copy.
// Empty PEM fields mean "leave the stored pair alone", so a form that does not
// re-send a private key does not silently delete one; ClearTLS is the separate,
// explicit way to remove it, because an empty string cannot mean both.
func merge(current *entities.Config, change Change) (*entities.Config, error) {
	out := *current
	out.Enabled = change.Enabled
	out.BindScope = change.BindScope
	out.Port = change.Port
	out.AllowInsecureAuth = change.AllowInsecureAuth

	suppliedCert := change.TLSCertPEM != ""
	suppliedKey := change.TLSKeyPEM != ""

	switch {
	case change.ClearTLS && (suppliedCert || suppliedKey):
		return nil, ErrClearAndSetTLS

	case change.ClearTLS:
		out.TLSCertPEM = ""
		out.TLSKeyPEM = ""

	case suppliedCert != suppliedKey:
		return nil, ErrTLSPairRequired

	case suppliedCert && suppliedKey:
		out.TLSCertPEM = change.TLSCertPEM
		out.TLSKeyPEM = change.TLSKeyPEM
	}

	return &out, nil
}

func (u *usecase) Reconcile(ctx context.Context) error {
	if u.flags != nil || u.supervisor == nil {
		return nil
	}

	cfg, err := u.stored(ctx)
	if err != nil {
		return err
	}

	if err := cfg.Validate(); err != nil {
		// A stored row that no longer validates is not applied. The listener
		// that is already running keeps running: refusing to change is safer
		// than tearing down a working, previously-validated listener because a
		// row was edited by hand into something this build rejects.
		return fmt.Errorf("stored SMTP submission configuration is not usable: %w", err)
	}

	return u.supervisor.Apply(ctx, cfg)
}

func (u *usecase) Run(ctx context.Context) {
	if u.flags != nil || u.supervisor == nil {
		return
	}

	ticker := time.NewTicker(u.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := u.Reconcile(ctx); err != nil && ctx.Err() == nil {
				u.logger.Warn("SMTP submission reconcile failed", "error", err)
			}
		}
	}
}
