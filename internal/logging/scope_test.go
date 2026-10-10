package logging

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/auth/middlewares"
)

// Unsubscribing while the gateway logs used to close a channel that a
// concurrent Write had already picked up, and the send that followed
// panicked inside the slog handler.
func TestSubscribeCancelWhileWritingNeverPanics(t *testing.T) {
	s := newLogStore(t)

	stop := make(chan struct{})
	var writers sync.WaitGroup
	for range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Write(LogEntry{ID: uuid.New().String(), Timestamp: time.Now()})
				}
			}
		}()
	}

	for range 2000 {
		ctx, cancel := context.WithCancel(context.Background())
		s.Subscribe(ctx)
		cancel()
	}
	close(stop)
	writers.Wait()
}

func TestListShowsATenantOnlyItsOwnEntries(t *testing.T) {
	s := newLogStore(t)

	ids := map[string]string{}
	for _, tenant := range []string{"tenant-a", "tenant-b", ""} {
		e := LogEntry{ID: uuid.New().String(), TenantID: tenant, Timestamp: time.Now(), Message: "m"}
		if err := s.Write(e); err != nil {
			t.Fatal(err)
		}
		ids[e.ID] = tenant
	}
	waitFor(t, func() bool {
		all, _, _ := s.List(100, "", "")
		return len(all) == 3
	})

	got, _, err := s.List(100, "", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TenantID != "tenant-a" {
		t.Fatalf("tenant-a saw %+v; want only its own entry, not another tenant's or a process-wide one", got)
	}
}

func TestHandlerRecordsTheAuthenticatedTenant(t *testing.T) {
	s := newLogStore(t)
	logger := slog.New(NewPebbleHandler(s, slog.LevelInfo))

	ctx := middlewares.WithPrincipal(context.Background(), &middlewares.Principal{
		Kind: middlewares.PrincipalUser, TenantID: "from-ctx", Role: middlewares.RoleAdmin,
	})
	logger.InfoContext(ctx, "with context")
	logger.With("tenant_id", "from-attr").Info("with attribute")

	waitFor(t, func() bool {
		all, _, _ := s.List(100, "", "")
		return len(all) == 2
	})
	for _, tenant := range []string{"from-ctx", "from-attr"} {
		got, _, _ := s.List(100, "", tenant)
		if len(got) != 1 {
			t.Errorf("%s: got %d entries, want 1", tenant, len(got))
		}
	}
}

func TestOnlyASuperAdminGetsTheUnfilteredView(t *testing.T) {
	cases := []struct {
		role, tenant string
		want         string
		wantErr      bool
	}{
		{middlewares.RoleSuperAdmin, "t1", "", false},
		{middlewares.RoleAdmin, "t1", "t1", false},
		{middlewares.RoleAdmin, "", "", true},
	}
	for _, c := range cases {
		ctx := middlewares.WithPrincipal(context.Background(), &middlewares.Principal{
			Kind: middlewares.PrincipalUser, TenantID: c.tenant, Role: c.role,
		})
		got, err := logScope(ctx)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s/%q: got %q, %v; want %q, err=%v", c.role, c.tenant, got, err, c.want, c.wantErr)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
