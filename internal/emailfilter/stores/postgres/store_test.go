package postgres

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/db"
)

func patternRule(tenant string, priority int) *emailfilter.Rule {
	return &emailfilter.Rule{
		ID: uuid.New().String(), TenantID: tenant, Name: fmt.Sprintf("rule %d", priority),
		Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
		Priority: priority, Enabled: true,
		Conditions: []emailfilter.Condition{
			{Field: emailfilter.FieldSubject, Operator: emailfilter.OpMatches, Values: []string{`wire\s+transfer`, `invoice\s+\d+`}},
		},
		Exceptions: []emailfilter.Condition{
			{Field: emailfilter.FieldFrom, Operator: emailfilter.OpMatches, Values: []string{`^ceo@`, `@trusted\.example$`}},
		},
	}
}

// Enabled runs for every message sent and received. It used to be a round trip
// and a recompile of every pattern each time: twenty rules of two patterns cost
// 2,602 allocations a call. The compiled rules are cached, so a message after
// the first costs a map lookup.
func TestEnabledRulesAreCompiledOnceAndReused(t *testing.T) {
	ctx := t.Context()
	store := NewRuleStore(storetest.NewConnection(t))
	for i := range 20 {
		if err := store.Create(ctx, patternRule(storetest.TenantA, i)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	rules, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound)
	if err != nil || len(rules) != 20 {
		t.Fatalf("Enabled = %d rules, %v", len(rules), err)
	}
	// Compiled: a pattern condition matches. An uncompiled rule never would.
	decision := emailfilter.Evaluate(rules, emailfilter.DirectionOutbound,
		emailfilter.Message{From: "bob@example.com", Subject: "Re: wire  transfer"})
	if decision.Action != emailfilter.ActionHold {
		t.Fatalf("decision = %+v; the cached rules' patterns are not compiled", decision)
	}

	allocs := testing.AllocsPerRun(50, func() {
		if _, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 10 {
		t.Errorf("a repeat Enabled allocated %.0f times; the rules are being read and compiled again", allocs)
	}
}

// A write through this instance takes effect at once, not after the TTL.
func TestARuleChangeIsSeenAtOnce(t *testing.T) {
	ctx := t.Context()
	store := NewRuleStore(storetest.NewConnection(t))
	enabled := func() int {
		t.Helper()
		rules, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound)
		if err != nil {
			t.Fatalf("Enabled: %v", err)
		}
		return len(rules)
	}

	if n := enabled(); n != 0 { // caches the empty set
		t.Fatalf("%d rules before any were created", n)
	}

	rule := patternRule(storetest.TenantA, 1)
	if err := store.Create(ctx, rule); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n := enabled(); n != 1 {
		t.Fatalf("after Create: %d rules, want 1; the cached empty set was served", n)
	}

	rule.Enabled = false
	if err := store.Update(ctx, rule); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if n := enabled(); n != 0 {
		t.Fatalf("after disabling: %d rules, want 0", n)
	}

	rule.Enabled = true
	if err := store.Update(ctx, rule); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if n := enabled(); n != 1 {
		t.Fatalf("after re-enabling: %d rules, want 1", n)
	}

	if err := store.Delete(ctx, storetest.TenantA, rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := enabled(); n != 0 {
		t.Fatalf("after Delete: %d rules, want 0", n)
	}
}

// The cache is per tenant: one tenant's rules never answer for another's.
func TestCachedRulesStayWithTheirTenant(t *testing.T) {
	ctx := t.Context()
	store := NewRuleStore(storetest.NewConnection(t))
	if err := store.Create(ctx, patternRule(storetest.TenantA, 1)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound); err != nil {
		t.Fatal(err)
	}
	rules, err := store.Enabled(ctx, storetest.TenantB, emailfilter.DirectionOutbound)
	if err != nil || len(rules) != 0 {
		t.Fatalf("tenant B sees %d rules, %v; want none", len(rules), err)
	}
	inbound, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionInbound)
	if err != nil || len(inbound) != 0 {
		t.Fatalf("inbound sees %d outbound rules, %v", len(inbound), err)
	}
}

// A rule stored invalid still fails the read that evaluates it, as it did when
// every read validated: a filter that silently drops a rule fails open. And it
// can still be listed, so it can be fixed.
func TestAStoredInvalidRuleFailsEnabledButCanBeListed(t *testing.T) {
	ctx := t.Context()
	conn := storetest.NewConnection(t)
	store := NewRuleStore(conn)
	rule := patternRule(storetest.TenantA, 1)
	if err := store.Create(ctx, rule); err != nil {
		t.Fatalf("Create: %v", err)
	}
	corrupt(t, conn, rule.ID)

	if _, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound); err == nil {
		t.Fatal("Enabled accepted a rule whose pattern does not compile")
	}
	listed, err := store.List(ctx, storetest.TenantA)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %d rules, %v; a broken rule has to be listable to be fixed", len(listed), err)
	}
}

func corrupt(t *testing.T, conn db.Connection, id string) {
	t.Helper()
	if _, err := conn.GetDB().ExecContext(t.Context(),
		`UPDATE filter_rules SET conditions = $1 WHERE id = $2`,
		`[{"field":"subject","operator":"matches","values":["(unclosed"]}]`, id); err != nil {
		t.Fatalf("corrupting the rule: %v", err)
	}
}
