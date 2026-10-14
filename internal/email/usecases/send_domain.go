package usecases

import (
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/email_provider/senders"
)

// senderAddress returns the lower-cased address of a From header value.
//
// It is the one place that decides who a send is from, and both halves of the
// send path call it: admission, to refuse a doomed send while someone is still
// waiting for the answer, and delivery, which stays the authority because a
// provider's configuration can change in between. Two extractors would be two
// answers, and admission would start refusing mail that delivery accepts, or
// the reverse.
//
// It accepts the display-name form ("Alice <alice@example.com>"), because
// admission already does. Splitting the raw string on "@" instead read that
// address's domain as `example.com>`, so a restricted provider refused every
// such send at delivery, after admission had answered 200.
func senderAddress(from string) (string, error) {
	return senders.Address(from)
}

// providerSenderMatch reports how a provider's allowed senders treat an
// address. An empty list means the operator has not restricted the provider.
func providerSenderMatch(p *providerEntities.EmailProvider, address string) senders.Match {
	return senders.MatchAddress(p.AllowedSenders, address)
}
