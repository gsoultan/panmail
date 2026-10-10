package postgres

import (
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/emailfilter"
	"github.com/gsoultan/panmail/internal/storetest"
)

// BenchmarkEnabledRules measures the rule read every sent and received message
// makes. After the first fill it is a cache hit; a change that reads or
// compiles the rules again per message shows here as a jump of three orders of
// magnitude, and allocs/op in particular.
func BenchmarkEnabledRules(b *testing.B) {
	ctx := context.Background()
	store := NewRuleStore(storetest.NewConnection(b))
	for i := range 20 {
		if err := store.Create(ctx, patternRule(storetest.TenantA, i)); err != nil {
			b.Fatalf("Create: %v", err)
		}
	}
	msg := emailfilter.Message{From: "bob@example.com", Subject: "Re: wire  transfer"}

	b.ReportAllocs()
	for b.Loop() {
		rules, err := store.Enabled(ctx, storetest.TenantA, emailfilter.DirectionOutbound)
		if err != nil {
			b.Fatalf("Enabled: %v", err)
		}
		if emailfilter.Evaluate(rules, emailfilter.DirectionOutbound, msg).Action != emailfilter.ActionHold {
			b.Fatal("the cached rules did not match; their patterns are not compiled")
		}
	}
}
