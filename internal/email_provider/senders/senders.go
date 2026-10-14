// Package senders decides which From addresses a provider may send as.
//
// A provider's allowed senders are a list of entries, each in one of two forms:
//
//	billing@example.com   that exact address
//	*@example.com         any address at exactly example.com
//
// An empty list means the operator has not restricted the provider, and any
// From is accepted. Matching ignores case and the display name, and a domain
// entry does not reach subdomains: *@example.com does not admit
// mail.example.com.
//
// The list replaced AllowedDomains, which held bare domains. Rows written
// before then still carry them, so Canonical reads "example.com" as
// "*@example.com" and the stored column needs no migration.
package senders

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gsoultan/gsmail"
)

// wildcard is the local part that stands for every address at a domain.
const wildcard = "*"

var (
	// domainPattern is a hostname of at least two labels, already lower-cased.
	// Internationalised domains are accepted in their punycode form.
	domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

	// localPattern is an unquoted RFC 5322 local part. Quoted local parts are
	// legal and vanishingly rare in a From; refusing them keeps the entry a
	// plain string an operator can read.
	localPattern = regexp.MustCompile("^[a-z0-9.!#$%&'+/=?^_`{|}~-]{1,64}$")
)

// Match is how an address relates to a list of allowed senders.
type Match int

const (
	// Refused: the list is restricted and nothing in it admits the address.
	Refused Match = iota
	// Unrestricted: the list is empty, so every address is admitted.
	Unrestricted
	// ByDomain: a *@domain entry admits the address.
	ByDomain
	// ByAddress: the address itself is in the list.
	ByAddress
)

// Allowed reports whether the match admits the address.
func (m Match) Allowed() bool { return m != Refused }

// InvalidEntryError reports an entry Normalize cannot accept, naming it so the
// caller can find it in a list of hundreds.
type InvalidEntryError struct {
	Entry  string
	Reason string
}

func (e *InvalidEntryError) Error() string {
	return fmt.Sprintf("invalid allowed sender %q: %s", e.Entry, e.Reason)
}

// Normalize validates entries from a caller and returns them in canonical
// form: lower case, display names removed, bare domains and "@domain" written
// as "*@domain", duplicates dropped, order kept. Blank entries are skipped.
//
// It is the write path's gate, so it refuses anything Match could not use
// rather than storing it to be ignored later: a typo in an allow-list is a
// sender silently refused at delivery.
func Normalize(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, raw := range entries {
		entry, err := normalizeEntry(raw)
		if err != nil {
			return nil, err
		}
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out, nil
}

func normalizeEntry(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "<") {
		if a, err := gsmail.ParseEmailAddress(s); err == nil && a != nil {
			s = strings.ToLower(a.Address)
		}
	}

	invalid := func(reason string) (string, error) {
		return "", &InvalidEntryError{Entry: strings.TrimSpace(raw), Reason: reason}
	}

	switch {
	case s == wildcard || s == "*@" || s == "*@*":
		return invalid("a lone wildcard would admit every address; leave the list empty instead")
	case strings.HasPrefix(s, "@"):
		s = wildcard + s
	case !strings.Contains(s, "@"):
		if strings.Contains(s, wildcard) {
			return invalid("wildcards are only supported as *@domain")
		}
		s = wildcard + "@" + s
	}

	at := strings.LastIndex(s, "@")
	local, domain := s[:at], s[at+1:]
	if local == "" || domain == "" {
		return invalid("use name@domain for one address or *@domain for a whole domain")
	}
	if (local != wildcard && strings.Contains(local, wildcard)) || strings.Contains(domain, wildcard) {
		return invalid("wildcards are only supported as *@domain")
	}
	if !domainPattern.MatchString(domain) {
		return invalid("not a valid domain")
	}
	if local != wildcard && !localPattern.MatchString(local) {
		return invalid("not a valid email address")
	}
	return local + "@" + domain, nil
}

// Canonical reads a stored list. It is lenient where Normalize is strict: an
// entry that no longer validates is kept as written rather than dropped, since
// dropping one would widen the list, and an empty result would lift the
// restriction entirely. Bare domains written before entries could be addresses
// become *@domain.
func Canonical(stored []string) []string {
	if len(stored) == 0 {
		return nil
	}
	out := make([]string, 0, len(stored))
	seen := make(map[string]bool, len(stored))
	for _, raw := range stored {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if !strings.Contains(s, "@") {
			s = wildcard + "@" + strings.Trim(s, ".")
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Address extracts the lower-cased address from a From header value.
//
// It accepts the display-name form ("Alice <alice@example.com>"). Splitting the
// raw string on "@" instead read that address's domain as "example.com>", so a
// restricted provider refused every such send.
func Address(from string) (string, error) {
	addr := strings.TrimSpace(from)
	if a, err := gsmail.ParseEmailAddress(addr); err == nil && a != nil {
		addr = a.Address
	}
	// The last "@": a quoted local part may contain one of its own, and the
	// domain is always what follows the final one.
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return "", fmt.Errorf("invalid from address: %s", from)
	}
	return strings.ToLower(addr), nil
}

// Domain is the part of an address or entry after its last "@".
func Domain(addressOrEntry string) string {
	return addressOrEntry[strings.LastIndex(addressOrEntry, "@")+1:]
}

// MatchAddress reports how a lower-cased address relates to a list. The list is
// read through Canonical, so a stored bare domain still counts.
func MatchAddress(entries []string, address string) Match {
	if len(entries) == 0 {
		return Unrestricted
	}
	address = strings.ToLower(address)
	domainEntry := wildcard + "@" + Domain(address)
	match := Refused
	for _, e := range Canonical(entries) {
		switch e {
		case address:
			return ByAddress
		case domainEntry:
			match = ByDomain
		}
	}
	return match
}

// Domains lists every domain an entry names, in order and once each: the From
// domains the provider may send as, whether by wildcard or by address.
func Domains(entries []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range Canonical(entries) {
		d := Domain(e)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
