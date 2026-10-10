package middlewares

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/usecases"
	"github.com/gsoultan/panmail/pkg/auth"
)

// X-Tenant-ID decides which tenant's data a request reads and writes. Before
// memberships it was honoured for super admins only; now any user may carry it
// into a tenant they belong to, which makes this the boundary that keeps one
// customer's mail out of another's console.

// fakeMemberships answers RoleIn from a table, and counts the calls so the
// caching claim can be checked rather than assumed. Like the real usecase, a
// super admin holds that role in every tenant and a user that no longer
// exists is an error.
type fakeMemberships struct {
	roles       map[string]string // "user|tenant" -> role
	superAdmins map[string]bool
	deleted     map[string]bool
	err         error
	calls       int
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
	if f.deleted[userID] {
		return "", usecases.ErrUserNotFound
	}
	if f.superAdmins[userID] {
		return entities.RoleSuperAdmin, nil
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
// A lookup that fails is an outage, not a verdict: nothing is served, but the
// answer is 503 rather than 401. Every console request now asks this question,
// home tenant included, and a 401 signs the console out, so a database blip
// would otherwise sign out every user at once.
func TestAMembershipLookupFailureIsUnavailableNotUnauthorized(t *testing.T) {
	for _, header := range []string{"guest", ""} {
		memberships := &fakeMemberships{err: errors.New("database is down")}
		m, maker := newMembershipMiddleware(t, memberships)

		seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, header)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("header %q: status = %d, want 503", header, code)
		}
		if seen != nil {
			t.Fatalf("header %q: the handler ran despite the membership lookup failing", header)
		}
	}
}

// Super admin is global, so support and monitoring keep working without a
// membership row in every tenant.
//
// This used to also assert that a super admin was answered without any
// lookup, trusting the role in the token. That trust is what let a super admin
// who had been demoted keep switching into every tenant for the rest of their
// 24-hour session; the global role is now confirmed from the account, through
// the same short-lived cache as every other answer.
func TestSuperAdminStillSwitchesWithoutAMembership(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{}, superAdmins: map[string]bool{"root": true}}
	m, maker := newMembershipMiddleware(t, memberships)

	for range 3 {
		seen, code := serveWithTenantHeader(t, m, maker, "root", "home", RoleSuperAdmin, "any-tenant")
		if code != http.StatusOK || seen == nil {
			t.Fatalf("status = %d, principal = %v; want a super admin to pass", code, seen)
		}
		if seen.TenantID != "any-tenant" || seen.Role != RoleSuperAdmin {
			t.Errorf("principal = %s/%s, want any-tenant/super admin", seen.TenantID, seen.Role)
		}
	}
	if memberships.calls != 1 {
		t.Errorf("membership was queried %d times across three requests, want 1 — the answer is cached", memberships.calls)
	}
}

// The ordinary request — no header, or one naming the token's own tenant — is
// the hot path. It used to be answered from the token alone and never query;
// it now asks once per cache lifetime, so that what the account is now, not
// what it was at sign-in, decides what the request may do.
func TestTheOrdinaryRequestIsAnsweredFromTheCache(t *testing.T) {
	for _, header := range []string{"", "home"} {
		memberships := &fakeMemberships{roles: map[string]string{"u1|home": entities.RoleViewer}}
		m, maker := newMembershipMiddleware(t, memberships)

		for range 3 {
			seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleViewer, header)
			if code != http.StatusOK || seen == nil {
				t.Fatalf("header %q: status = %d, principal = %v; want the request served", header, code, seen)
			}
			if seen.TenantID != "home" || seen.Role != entities.RoleViewer {
				t.Errorf("header %q: principal = %s/%s, want home/viewer", header, seen.TenantID, seen.Role)
			}
		}
		if memberships.calls != 1 {
			t.Errorf("header %q: %d membership queries across three requests, want 1", header, memberships.calls)
		}
	}
}

// A session used to run on the token's payload for its whole 24 hours, so
// deleting or demoting an account changed nothing until it expired. These are
// the three changes that must now land within the cache's lifetime.
func TestTheAccountAsItIsNowDecidesTheRequest(t *testing.T) {
	t.Run("a deleted user is refused in their own tenant", func(t *testing.T) {
		memberships := &fakeMemberships{deleted: map[string]bool{"u1": true}}
		m, maker := newMembershipMiddleware(t, memberships)

		seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "")
		if code != http.StatusUnauthorized || seen != nil {
			t.Fatalf("status = %d, principal = %v; a deleted user's token still worked", code, seen)
		}
	})

	t.Run("a demoted user acts with their new role", func(t *testing.T) {
		memberships := &fakeMemberships{roles: map[string]string{"u1|home": entities.RoleViewer}}
		m, maker := newMembershipMiddleware(t, memberships)

		seen, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "")
		if code != http.StatusOK || seen == nil {
			t.Fatalf("status = %d, want the request served", code)
		}
		if seen.Role != entities.RoleViewer {
			t.Errorf("role = %q, want viewer — the token's administrator role outlived the demotion", seen.Role)
		}
	})

	t.Run("a former super admin can no longer enter any tenant", func(t *testing.T) {
		memberships := &fakeMemberships{roles: map[string]string{"root|home": entities.RoleAdmin}}
		m, maker := newMembershipMiddleware(t, memberships)

		seen, code := serveWithTenantHeader(t, m, maker, "root", "home", RoleSuperAdmin, "someone-elses")
		if code != http.StatusUnauthorized || seen != nil {
			t.Fatalf("status = %d, principal = %v; a demoted super admin still switched tenant", code, seen)
		}
		seen, code = serveWithTenantHeader(t, m, maker, "root", "home", RoleSuperAdmin, "")
		if code != http.StatusOK || seen == nil || seen.Role != entities.RoleAdmin {
			t.Fatalf("status = %d, principal = %v; want them served at home as an administrator", code, seen)
		}
	})
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

// The tenant id is whatever the caller's header says. Caching a refusal under
// it let any viewer fill the shared cache with invented ids until it stopped
// accepting entries, and real members' switches went back to the database.
func TestRefusedTenantsDoNotFillTheCache(t *testing.T) {
	memberships := &fakeMemberships{roles: map[string]string{"u1|guest": entities.RoleViewer}}
	m, maker := newMembershipMiddleware(t, memberships)

	for i := range 50 {
		header := fmt.Sprintf("invented-%d", i)
		if _, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, header); code != http.StatusUnauthorized {
			t.Fatalf("status = %d for an invented tenant, want 401", code)
		}
	}
	if got := m.resolvedRoles.Len(); got != 0 {
		t.Errorf("%d refusals cached; only real memberships should occupy the cache", got)
	}

	if _, code := serveWithTenantHeader(t, m, maker, "u1", "home", entities.RoleAdmin, "guest"); code != http.StatusOK {
		t.Fatalf("status = %d for a real membership, want 200", code)
	}
	if got := m.resolvedRoles.Len(); got != 1 {
		t.Errorf("%d entries cached after one real switch, want 1", got)
	}
}
