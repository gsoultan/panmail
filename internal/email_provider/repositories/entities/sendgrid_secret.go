package entities

import (
	"regexp"
	"strings"
)

// sendGridInboundToken finds the inbound credential in a SendGrid provider's
// webhook secret: a whitespace-separated token starting "inbound=". Base64
// and PEM never contain one, so a key and a credential cannot be mistaken
// for each other.
var sendGridInboundToken = regexp.MustCompile(`(?:^|\s)inbound=(\S*)`)

// SplitSendGridWebhookSecret separates the two things a SendGrid provider's
// webhook secret may hold:
//
//   - the event webhook's verification key, SendGrid's ECDSA *public* key; and
//   - the HTTP Basic credential its Inbound Parse posts with, written
//     "inbound=user:pass" (or "inbound=pass" for a password alone).
//
// They share the one field because both are how this provider proves a post
// to panmail is its own, and a second column for one provider type is schema
// nobody else needs. The public key cannot double as the inbound credential:
// it is public, and anyone who had seen it could post mail.
//
// Either part may be absent. "MFkw... inbound=parse:s3cret" holds both.
func SplitSendGridWebhookSecret(secret string) (publicKey, inboundCredential string) {
	m := sendGridInboundToken.FindStringSubmatchIndex(secret)
	if m == nil {
		return strings.TrimSpace(secret), ""
	}
	inboundCredential = secret[m[2]:m[3]]
	publicKey = strings.TrimSpace(secret[:m[0]] + " " + secret[m[1]:])
	return publicKey, inboundCredential
}
