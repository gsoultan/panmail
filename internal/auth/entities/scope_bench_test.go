package entities

import (
	"slices"
	"testing"
)

// These exist to stop a plausible-sounding "optimisation" from landing.
//
// Both functions look like textbook candidates for a map: IsValidScope is a
// linear scan, and NormalizeScopes is quadratic in the grant size. Measured at
// the sizes that actually occur, neither change pays. Indexing AllScopes into a
// map is faster in isolation (~5.4ns against ~7.8ns) but not once it is called
// from NormalizeScopes, where the fourteen-entry slice is already in cache and
// the map costs an indirection. Deduplicating through a `seen` map is worse
// still — the per-call allocation outweighs a scan over at most fourteen
// entries, by roughly 50% on a full grant.
//
// Rerun with -benchmem before changing either. Both run on a cache miss or when
// minting a key, not per request: VerifyApiKey answers from a TTL cache.

func BenchmarkIsValidScope(b *testing.B) {
	b.Run("hit", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = IsValidScope(ScopeFiltersRelease)
		}
	})
	b.Run("miss", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = IsValidScope(Scope("nope:nope"))
		}
	})
}

func BenchmarkNormalizeScopes(b *testing.B) {
	// What a key is usually granted.
	b.Run("typical", func(b *testing.B) {
		in := []Scope{ScopeEmailSend, ScopeTemplatesRead, ScopeSuppressionsRead}
		for i := 0; i < b.N; i++ {
			_ = NormalizeScopes(in)
		}
	})
	// The worst case a caller can ask for.
	b.Run("full", func(b *testing.B) {
		in := slices.Clone(AllScopes)
		for i := 0; i < b.N; i++ {
			_ = NormalizeScopes(in)
		}
	})
}
