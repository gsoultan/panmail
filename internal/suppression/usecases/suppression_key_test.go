package usecases

import "testing"

// The address a suppression is stored under has to be the address the send path
// looks it up by, and the two are computed in different packages.
//
// Storing happens here, through suppressionKey. Looking up happens in the send
// path's isSuppressed. Both call gsmail.NormalizeAddress today, which is why
// the chain works — an unsubscribe click stores one spelling and a later send
// with a different spelling still finds it.
//
// TestSuppressionMatchesRegardlessOfSpelling already covers the pair inside
// this package, and it does catch a regression to plain strings.ToLower — it
// stores a display-name form and checks a plain one, so the two inputs differ
// and a weaker normaliser fails it. I assumed otherwise and checked; it earns
// its place.
//
// What it does not cover is what the key actually is, that two different
// addresses cannot collide on one, and that an unparseable address does not
// key to the empty string — which would give every malformed recipient a
// single shared suppression entry.

func TestEverySpellingOfAnAddressStoresUnderOneKey(t *testing.T) {
	const canonical = "person+tag@example.org"

	for _, spelling := range []string{
		"person+tag@example.org",
		"Person+Tag@Example.ORG",              // a caller's casing
		"  person+tag@example.org",            // whitespace from a pasted list
		`"A Person" <person+tag@example.org>`, // the display-name form a mail client produces
		"<person+tag@example.org>",
	} {
		if got := suppressionKey(spelling); got != canonical {
			t.Errorf("suppressionKey(%q) = %q, want %q — a send using this spelling "+
				"would not find the suppression and would deliver to someone who unsubscribed",
				spelling, got, canonical)
		}
	}
}

// Different addresses must not collapse onto one key, or unsubscribing one
// recipient would silently stop mail to another.
func TestDifferentAddressesKeepDifferentKeys(t *testing.T) {
	distinct := []string{
		"person@example.org",
		"person+tag@example.org", // plus-addressing is a different mailbox to some providers
		"person@example.com",
		"other@example.org",
	}

	seen := map[string]string{}
	for _, addr := range distinct {
		key := suppressionKey(addr)
		if previous, clash := seen[key]; clash {
			t.Errorf("%q and %q both key to %q; suppressing one would stop mail to the other",
				previous, addr, key)
		}
		seen[key] = addr
	}
}

// An address that cannot be parsed still has to produce a stable key rather
// than an empty one — an empty key would suppress the empty address, which
// every unparseable recipient would then match.
func TestAnUnparseableAddressDoesNotCollapseToEmpty(t *testing.T) {
	for _, junk := range []string{"not an address", "@", "person@"} {
		if suppressionKey(junk) == "" {
			t.Errorf("suppressionKey(%q) is empty; every malformed address would share one "+
				"suppression entry", junk)
		}
	}
}
