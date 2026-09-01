package entities

import "slices"

// Scope is a capability an API key may hold. Keys are granted an explicit set
// rather than inheriting a user role, so a key minted to send mail cannot also
// rewrite the tenant's providers or webhooks.
type Scope string

const (
	ScopeEmailSend         Scope = "email:send"
	ScopeProvidersRead     Scope = "providers:read"
	ScopeProvidersWrite    Scope = "providers:write"
	ScopeTemplatesRead     Scope = "templates:read"
	ScopeTemplatesWrite    Scope = "templates:write"
	ScopeSuppressionsRead  Scope = "suppressions:read"
	ScopeSuppressionsWrite Scope = "suppressions:write"
	ScopeWebhooksRead      Scope = "webhooks:read"
	ScopeWebhooksWrite     Scope = "webhooks:write"
	ScopeEventsRead        Scope = "events:read"
	ScopeInboundRead       Scope = "inbound:read"
	ScopeFiltersRead       Scope = "filters:read"
	ScopeFiltersWrite      Scope = "filters:write"
	// ScopeFiltersRelease is separate from write on purpose.
	// Editing a rule changes what happens next; releasing a held
	// message puts mail on the wire now, and those are not the same
	// trust. An integration that manages rules should not be able to
	// empty the quarantine.
	ScopeFiltersRelease Scope = "filters:release"
)

// AllScopes is the set a caller may choose from when minting a key.
var AllScopes = []Scope{
	ScopeEmailSend,
	ScopeProvidersRead,
	ScopeProvidersWrite,
	ScopeTemplatesRead,
	ScopeTemplatesWrite,
	ScopeSuppressionsRead,
	ScopeSuppressionsWrite,
	ScopeWebhooksRead,
	ScopeWebhooksWrite,
	ScopeEventsRead,
	ScopeInboundRead,
	ScopeFiltersRead,
	ScopeFiltersWrite,
	ScopeFiltersRelease,
}

// DefaultScopes is what a key receives when none are requested. Sending mail is
// the reason API keys exist; everything else must be asked for.
var DefaultScopes = []Scope{ScopeEmailSend}

// IsValidScope reports whether s is a scope this server recognises.
func IsValidScope(s Scope) bool {
	return slices.Contains(AllScopes, s)
}

// NormalizeScopes drops unknown and duplicate scopes, falling back to the
// default set when nothing usable remains.
func NormalizeScopes(requested []Scope) []Scope {
	var out []Scope
	for _, s := range requested {
		if IsValidScope(s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return slices.Clone(DefaultScopes)
	}
	return out
}
