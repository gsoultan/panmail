package usecases

import (
	"fmt"
	"strings"

	"github.com/gsoultan/gsmail"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
)

// senderDomain returns the lower-cased domain of a From address.
//
// It is the one place that decides what domain a send is from, and both halves
// of the send path call it: admission, to refuse a doomed send while someone
// is still waiting for the answer, and delivery, which stays the authority
// because a provider's configuration can change in between. Two extractors
// would be two answers, and admission would start refusing mail that delivery
// accepts, or the reverse.
//
// It accepts the display-name form ("Alice <alice@example.com>"), because
// admission already does. Splitting the raw string on "@" instead -- which is
// what this replaced -- read that address's domain as `example.com>`, so a
// provider with AllowedDomains refused every such send at delivery, after
// admission had answered 200.
func senderDomain(from string) (string, error) {
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
	return strings.ToLower(addr[at+1:]), nil
}

// providerAllowsDomain reports whether a provider may send for a domain. An
// empty AllowedDomains means the operator has not restricted the provider.
func providerAllowsDomain(p *providerEntities.EmailProvider, domain string) bool {
	if len(p.AllowedDomains) == 0 {
		return true
	}
	for _, d := range p.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}
