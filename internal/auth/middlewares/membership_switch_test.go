package middlewares

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/pkg/auth"
)

// X-Tenant-ID decides which tenant's data a request reads and writes. Before
// memberships it was honoured for super admins only; now any user may carry it
// into a tenant they belong to, which makes this the boundary that keeps one
// customer's mail out of another's console.

// fakeMemberships answers RoleIn from a table, and counts the calls so the
// caching claim can be checked rather than assumed.
type fakeMemberships struct {
	roles map[string]string // "user|tenant" -> role
	err   error
	calls int
}

func (f *fakeMemberships) AssignUserToTenant(ctx context.Context, userID, tenantID, role string) (*entities.UserTenant, error) {
	return nil, nil
}
func (f *fakeMemberships) RemoveUserFromTenant(ctx context.Context, userID, tenantID string) error {
	return nil
}
func (f *fakeMemberships) ListUserTenants(ctx context.Context, userID string) ([]*entities.UserTenant, error) {
	return nil, nil
}
func (f *fakeMemberships) RoleIn(ctx context.Context, userID, tenantID string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.roles[userID+"|"+tenantID], nil
}

// serveWithTenantHeader runs one authenticated request and reports the
// principal the handler saw, or nil when the middleware refused.
func serveWithTenantHeader(t *testing.T, m *AuthMiddleware, maker auth.TokenMaker, userID, tokenTenant, role, header string) (*Principal, int) {
	t.Helper()

	token, err := maker.CreateToken(auth.TokenRequest{
		UserID:   userID,
		TenantID: tokenTenant,
		Role:     role,
		Purpose:  auth.PurposeSession,
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}

	var seen *Principal
	handler := m.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := GetPrincipal(r.Context()); ok {
			seen = p
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/panmail.v1.EmailService/ListEmails", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if header != "" {
		req.Header.Set(tenantIDHeader, header)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return seen, rec.Code
}

func newMembershipMiddleware(t *testing.T, memberships *fakeMemberships) (*AuthMiddleware, auth.TokenMaker) {
	t.Helper()
	maker := newTestTokenMaker(t)
	return NewAuthMiddleware(maker, nil).WithMemberships(memberships), maker
}

// The whole point of assignment: the account reaches the tenant it was lent to.
func TestSwitchingIntoAnAssignedTenantIsAllowed(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{"u1|guest": entities.RoleEditor}}
	m, maker := newMembershipMiddleware(t, memberships)

	seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "guest")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if seen == nil {
		t.Fatal("no principal reached the handler")
	}
	if seen.TenantID != "guest" {
		t.Errorf("tenant = %q, want guest", seen.TenantID)
	}
	// Carrying the home role across would make every assignment a promotion.
	if seen.Role != entities.RoleEditor {
		t.Errorf("role = %q, want %q — the role held in the tenant being entered, not the home role",
			seen.Role, entities.RoleEditor)
	}
}

// The refusal that matters: naming a tenant you have no standing in.
func TestSwitchingIntoAnUnassignedTenantIsRefused(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{}}
	m, maker := newMembershipMiddleware(t, memberships)

	seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "stranger")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — an administrator reached a tenant they do not belong to", code)
	}
	if seen != nil {
		t.Fatalf("the handler ran with tenant %q for a user who is not a member", seen.TenantID)
	}
}

// A membership lookup that fails is not permission. Treating an unreadable
// answer as a grant would turn a database blip into a cross-tenant read.
func TestAMembershipLookupFailureRefusesTheSwitch(t *testing.T) {
	memberships := &fakeMemberships{err: errors.New("database is down")}
	m, maker := newMembershipMiddleware(t, memberships)

	seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "guest")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if seen != nil {
		t.Fatal("the handler ran despite the membership lookup failing")
	}
}

// Super admin is global, so support and monitoring keep working without a
// membership row in every tenant — and without a query per request.
func TestSuperAdminStillSwitchesWithoutAMembership(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{}}
	m, maker := newMembershipMiddleware(t, memberships)

	seen, code := serveWithTenantHeader(t, m, maker, "root", "home", RoleSuperAdmin, "any-tenant")
	if code != http.StatusOK || seen == nil {
		t.Fatalf("status = %d, principal = %v; want a super admin to pass", code, seen)
	}
	if seen.TenantID != "any-tenant" {
		t.Errorf("tenant = %q, want any-tenant", seen.TenantID)
	}
	if memberships.calls != 0 {
		t.Errorf("membership was queried %d times for a super admin; the global role should answer without a lookup", memberships.calls)
	}
}

// The ordinary request — no header, or one naming the token's own tenant — is
// the hot path and must not pay for a feature it does not use.
func TestTheOrdinaryRequestNeverQueriesMembership(t *testing.T) {
	for _, header := range []string{"", "home"} {
		memberships := &fakeMemberships{roles: map[string]string{}}
		m, maker := newMembershipMiddleware(t, memberships)

		seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleViewer, header)
		if code != http.StatusOK || seen == nil {
			t.Fatalf("header %q: status = %d, principal = %v; want the request served", header, code, seen)
		}
		if seen.TenantID != "home" || seen.Role != entities.RoleViewer {
			t.Errorf("header %q: principal = %s/%s, want home/viewer", header, seen.TenantID, seen.Role)
		}
		if memberships.calls != 0 {
			t.Errorf("header %q: %d membership queries on a request that stays in its own tenant", header, memberships.calls)
		}
	}
}

// An instance wired without membership keeps the behaviour it had before:
// only a super admin may name another tenant.
func TestWithoutMembershipOnlySuperAdminMaySwitch(t *testing.T) {
	maker := newTestTokenMaker(t)
	m := NewAuthMiddleware(maker, nil)

	if _, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "guest"); code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an administrator when membership is not configured", code)
	}
	if seen, code := serveWithTenantHeader(t, m, maker, "root", "home", RoleSuperAdmin, "guest"); code != http.StatusOK || seen.TenantID != "guest" {
		t.Errorf("status = %d, principal = %v; want a super admin to still switch", code, seen)
	}
}

// The cache exists so that a user working inside a tenant they were lent does
// not pay two queries per request.
func TestRepeatedSwitchesReuseTheCachedMembership(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{"u1|guest": entities.RoleViewer}}
	m, maker := newMembershipMiddleware(t, memberships)

	for range 3 {
		if _, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "guest"); code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
	}
	if memberships.calls != 1 {
		t.Errorf("membership was queried %d times across three requests, want 1", memberships.calls)
	}
}
