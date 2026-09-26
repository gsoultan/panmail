package usecases

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/gsoultan/gsmail"
)

// errEmptyRecipient reports a recipient list with a blank entry in it.
var errEmptyRecipient = errors.New("recipient list contains an empty address")

// uniqueRecipients merges the address lists of a message into one lower-cased,
// de-duplicated slice, preserving the order they were given in.
//
// The same person may appear in more than one header; sending to them twice
// because of it is a bug the recipient notices.
func uniqueRecipients(lists ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string

	for _, list := range lists {
		for _, address := range list {
			address = strings.ToLower(strings.TrimSpace(address))
			if address == "" {
				continue
			}
			if _, ok := seen[address]; ok {
				continue
			}
			seen[address] = struct{}{}
			out = append(out, address)
		}
	}

	return out
}

// resolvedRecipients is a message's audience: every address it will be
// delivered to, once each, in the order the lists were given.
//
// addresses and normalised are parallel. addresses is what the send path
// reports and records events against; normalised is the addr-spec used to look
// the address up on the suppression list.
type resolvedRecipients struct {
	addresses  []string
	normalised []string
}

// resolveRecipients validates every address and derives both forms in a single
// pass.
//
// Parsing an address allocates, and it used to happen twice for every
// recipient — once to reject a malformed address, and again inside
// NormalizeAddress to derive the suppression key. An allocation profile of
// admission put net/mail's parser at 76% of everything the path allocated, so
// parsing once is most of that cost removed.
//
// The rules are unchanged. An empty entry is still a caller building the list
// wrongly rather than a malformed address, and is reported as such. A
// duplicate still collapses, because the same person in To and Cc should
// receive one copy, not two.
func resolveRecipients(lists ...[]string) (resolvedRecipients, error) {
	total := 0
	for _, list := range lists {
		total += len(list)
	}

	resolved := resolvedRecipients{
		addresses:  make([]string, 0, total),
		normalised: make([]string, 0, total),
	}
	seen := make(map[string]struct{}, total)

	for _, list := range lists {
		for _, address := range list {
			trimmed := strings.TrimSpace(address)
			if trimmed == "" {
				return resolvedRecipients{}, errEmptyRecipient
			}

			// ParseEmailAddress rather than ValidateEmailSyntax, because the
			// latter wants a bare addr-spec and callers legitimately send the
			// display-name form, "Alice Smith <alice@example.com>".
			parsed, err := gsmail.ParseEmailAddress(address)
			if err != nil {
				return resolvedRecipients{}, fmt.Errorf("invalid recipient %q: %w", address, err)
			}

			key := strings.ToLower(trimmed)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			// What NormalizeAddress would have returned, without paying for a
			// second parse of an address already parsed above.
			normal := keyFromParsed(key, parsed)

			resolved.addresses = append(resolved.addresses, key)
			resolved.normalised = append(resolved.normalised, normal)
		}
	}

	return resolved, nil
}

// keyFromParsed is the form a recipient is suppressed under, from an address
// already parsed. Admission and delivery both go through it -- delivery via
// suppressionKeyOf -- so the two cannot disagree about whether an address is
// on the list.
func keyFromParsed(lowered string, parsed *mail.Address) string {
	if parsed != nil {
		return strings.ToLower(strings.TrimSpace(parsed.Address))
	}
	return lowered
}

// suppressionKeyOf is keyFromParsed for an address that has not been parsed.
//
// Delivery needs it because its recipient list comes from uniqueRecipients,
// which only lowercases: "Bob <Bob@Example.com>" stays "bob <bob@example.com>",
// and a suppression is stored under "bob@example.com". Looking that up as-is
// would never match a display-name recipient, so a suppression recorded for
// one would be silently ignored at delivery.
func suppressionKeyOf(address string) string {
	lowered := strings.ToLower(strings.TrimSpace(address))
	parsed, err := gsmail.ParseEmailAddress(address)
	if err != nil {
		parsed = nil
	}
	return keyFromParsed(lowered, parsed)
}
