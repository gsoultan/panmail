package redact

import "regexp"

// Secrets that are recognisable without a label.
//
// The label-anchored patterns in redact.go can only find a credential that
// someone introduced — "Password: x". A great deal of what leaks into mail
// arrives with no introduction at all: a key pasted into a paragraph, a token
// in a support thread, a private key attached to a reply. Nothing in a label
// matcher will ever see those.
//
// What makes these safe to match without a label is that the shapes are issued,
// not chosen. `AKIA` followed by sixteen uppercase characters is an AWS access
// key or it is nothing; a PEM block header is not a phrase anyone writes by
// accident. That is the bar for adding to this list — a shape a person would
// have to be unlucky to type. Anything that merely *looks* random does not
// qualify, which is why high-entropy matching is deliberately absent: base64
// images, tracking URLs and message ids all look random, and a redactor that
// eats a tracking pixel is one people switch off.
//
// These fire from the Passwords level up, not only at Secrets. A private key in
// a message body is never something an operator wanted rendered, and the
// false-positive risk that justifies gating the wider label patterns does not
// exist here.
var shapePatterns = []*regexp.Regexp{
	// A JWT. Three dot-separated base64url segments beginning with the encoding
	// of `{"`, which is what every JOSE header starts with.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),

	// AWS access key id.
	regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b`),

	// GitHub personal access, OAuth, server-to-server, refresh and fine-grained
	// tokens. All carry their own prefix precisely so they can be spotted.
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,255}\b`),

	// Stripe.
	regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),

	// Slack bot, user, app and refresh tokens.
	regexp.MustCompile(`\bxox[baprse]-[A-Za-z0-9-]{10,}\b`),

	// Google API key.
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),

	// OpenAI and Anthropic.
	regexp.MustCompile(`\bsk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}\b`),

	// SendGrid, which panmail itself can be configured with.
	regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\b`),
}

// pemRe matches a PEM private key block of any flavour — RSA, EC, OPENSSH, PGP,
// and the unqualified PKCS#8 header.
//
// The header and footer are captured so they survive the mask. Leaving
// "-----BEGIN RSA PRIVATE KEY-----" in place tells an operator what the message
// carried and that redaction fired; replacing the whole block with five stars
// tells them nothing.
//
// Non-greedy body, so two keys in one message are two matches rather than one
// spanning both.
var pemRe = regexp.MustCompile(
	`(?s)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----).*?(-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)`)

// cardRe finds a candidate card number: 13 to 19 digits, optionally grouped by
// single spaces or hyphens. The checksum, not this, is what decides.
var cardRe = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)

// maskShapes replaces every recognisable credential, label or no.
func maskShapes(s string) string {
	if s == "" {
		return s
	}

	s = pemRe.ReplaceAllString(s, "${1}\n"+Mask+"\n${2}")

	for _, re := range shapePatterns {
		s = re.ReplaceAllString(s, Mask)
	}
	return s
}

// maskCards replaces digit runs that pass the Luhn checksum.
//
// Luhn is what makes this usable. Matching 13-to-19 digit runs alone would eat
// order numbers, tracking numbers and phone numbers; requiring the checksum
// means a sequence has to be a plausible card, and an arbitrary number passes
// only about one time in ten.
//
// It is still the widest thing here, which is why it sits at the Secrets level
// rather than at the default.
func maskCards(s string) string {
	if s == "" {
		return s
	}
	return cardRe.ReplaceAllStringFunc(s, func(candidate string) string {
		if !luhn(candidate) {
			return candidate
		}
		return Mask
	})
}

// luhn reports whether the digits in s satisfy the Luhn checksum, ignoring the
// spaces and hyphens people group card numbers with.
func luhn(s string) bool {
	sum, count, double := 0, 0, false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		count++
		double = !double
	}
	// A card is 13 to 19 digits. Shorter runs that happen to satisfy the
	// checksum are not card numbers, and a 4-digit "1234" passing Luhn is
	// exactly how a redactor starts eating order references.
	if count < 13 || count > 19 {
		return false
	}
	return sum%10 == 0
}
