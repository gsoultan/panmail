package emailutil

import (
	"strings"
)

// GetRootDomain extracts the root domain from a hostname or email address.
// e.g., "mail.example.com" -> "example.com", "user@example.com" -> "example.com"
func GetRootDomain(input string) string {
	domain := input
	if idx := strings.LastIndex(input, "@"); idx != -1 {
		domain = input[idx+1:]
	}

	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return strings.ToLower(domain)
	}

	// Simple logic for root domain: last two parts
	// In a real world, this should handle public suffix list (e.g. .co.uk)
	// But as per instructions "domain name not sub domain", matching last two is usually what's meant.
	return strings.ToLower(strings.Join(parts[len(parts)-2:], "."))
}
