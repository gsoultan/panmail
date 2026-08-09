package usecases

import "strings"

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
