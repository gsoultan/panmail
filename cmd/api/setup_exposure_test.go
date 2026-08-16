package main

import (
	"context"
	"errors"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	setupusecases "github.com/gsoultan/panmail/internal/setup/usecases"
)

// Setup takes no credentials, and it cannot: there is no account to
// authenticate against until it has run. So between deploying an instance and
// configuring it, whoever reaches it first chooses the administrator account
// and the database — which is to say, owns it. TestDatabaseConnection is open
// for the same reason and until the same moment, and it dials any host and port
// it is given.
//
// Both close permanently once setup completes. The window is real because it
// lasts until an operator gets round to it, and a container that came up an
// hour ago behind a public ingress has been open the whole time.
//
// A log line does not close it. It does mean the operator knows the clock is
// running, which is worth having and is what this covers.

type setupState struct {
	done bool
	err  error
}

func (s setupState) IsSetup(context.Context) (bool, error) { return s.done, s.err }
func (s setupState) Setup(context.Context, *panmailv1.DatabaseConfig, string, string, string, string) error {
	return errors.New("not used")
}
func (s setupState) TestDatabaseConnection(context.Context, *panmailv1.DatabaseConfig) error {
	return errors.New("not used")
}

var _ setupusecases.SetupUsecase = setupState{}

func TestTheOpenSetupWindowIsReported(t *testing.T) {
	if !setupIsStillOpen(setupState{done: false}) {
		t.Error("an unconfigured instance was not reported; anyone who reaches it can take it")
	}
}

func TestAConfiguredInstanceIsNotReported(t *testing.T) {
	if setupIsStillOpen(setupState{done: true}) {
		t.Error("a configured instance was reported as open; setup already refuses, " +
			"and a warning that is always on is one nobody reads")
	}
}

// Not knowing is not the same as being open. A database that is unreachable at
// startup is reported by readiness; guessing here would produce a scary warning
// on every restart of a healthy instance.
func TestAnUnknownStateIsNotReportedAsOpen(t *testing.T) {
	if setupIsStillOpen(setupState{err: errors.New("database not connected")}) {
		t.Error("an unknown setup state was reported as an open window")
	}
}
