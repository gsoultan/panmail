// Package redact masks secrets in a stored message body before it is shown
// back through the API.
//
// The problem it solves is specific. Transactional mail carries the things it
// carries — a password reset sends a password, a login sends a one-time code —
// and panmail keeps the body so an operator can answer "what did we actually
// send". That archive is readable from the dashboard months later by anyone
// with an account.
//
// Three rules shape everything here:
//
//   - **The label survives, the value does not.** Replacing the whole line
//     would tell an operator nothing; "Password: *****" tells them the message
//     contained one and that redaction fired.
//   - **Stored mail is never rewritten.** This runs on the read path only. A
//     redactor with a bug that ate content would otherwise destroy the archive
//     it exists to protect, irreversibly.
//   - **False positives are cheaper than false negatives**, but not free. A
//     message discussing password policy should survive, which is why matching
//     is anchored to a label followed by a delimiter rather than to the word
//     alone.
package redact

import (
	"regexp"
	"strings"
)

// Level is how hard to look. The zero value is Passwords rather than Off,
// because a caller that forgets to configure this should get the safe
// behaviour, not the exposing one.
type Level int

const (
	// Passwords masks the value after a password-like label. The default.
	Passwords Level = iota

	// Codes adds one-time codes, which are worth as much as a password for as
	// long as they are valid.
	Codes

	// Secrets adds bearer tokens and API keys.
	Secrets

	// Off shows the body as stored. Deliberately last so it cannot be the
	// zero value.
	Off
)

// Mask is what replaces a secret. Fixed width rather than proportional to the
// original: a mask that matched the length would leak it, and knowing a
// password was eight characters is a meaningful head start.
const Mask = "*****"

// A label, a delimiter, then the value to the end of the line or the next tag.
//
// The delimiter is required. Without it, "password" in "choose a strong
// password" matches and the sentence after it disappears. With it, the pattern
// only fires on the shape a credential is actually written in.
//
// (?i) throughout: labels arrive in every case a template author felt like.
var (
	passwordLabels = `pass(?:word|phrase|code)?|pwd|senha|contrase(?:n|ñ)a|mot de passe|kata sandi`
	codeLabels     = `otp|pin|(?:verification|security|confirmation|access|login|auth(?:entication)?|one[- ]time)\s*code|code|2fa`
	secretLabels   = `api[\s_-]*key|secret|bearer|token|client[\s_-]*secret|private[\s_-]*key`
)

// buildPattern renders the matcher for a set of labels.
//
// The value is everything up to a newline or a `<`, so a password sitting in a
// table cell stops at the closing tag rather than swallowing the rest of the
// document. Trailing whitespace is left outside the capture so the mask does
// not absorb the line break.
func buildPattern(labels string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(` + labels + `)(\s*(?::|=|is|:=)\s*)([^\r\n<]+)`)
}

var (
	passwordRe = buildPattern(passwordLabels)
	codeRe     = buildPattern(codeLabels)
	secretRe   = buildPattern(secretLabels)

	passwordHTMLRe = buildHTMLPattern(passwordLabels)
	codeHTMLRe     = buildHTMLPattern(codeLabels)
	secretHTMLRe   = buildHTMLPattern(secretLabels)
)

// buildHTMLPattern is buildPattern with room for tags between the label and
// the value.
//
// The two-column table is how HTML mail writes a credential —
// `<td>Password:</td><td>hunter2</td>` — and the plain pattern misses it,
// because the value does not follow the delimiter directly. Up to four
// intervening tags are allowed through and reproduced in the output, so the
// markup a redacted document renders is the markup it had.
//
// The delimiter is still required. Dropping it to also catch
// `<td>Password</td><td>hunter2</td>` would fire on any table with the word in
// a header cell, which is most password-change notifications.
func buildHTMLPattern(labels string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?i)\b(` + labels + `)(\s*(?::|=|is|:=)\s*)((?:</?[^>]{0,60}>\s*){0,4})([^\r\n<]+)`)
}

// buildCellPattern matches a label alone in one cell and a value in the next.
//
// `<td>Password</td><td>hunter2</td>` is the other half of how HTML mail writes
// a credential, and the delimiter-anchored pattern cannot see it because there
// is no delimiter — the table *is* the delimiter.
//
// The label cell must contain only the label, optionally with a colon and
// whitespace. That restriction is the whole safety argument: it fires on a
// layout table and not on a sentence, so "You can change your password here"
// in a cell does not take the cell beside it.
func buildCellPattern(labels string) *regexp.Regexp {
	return regexp.MustCompile(
		`(?i)(<(?:td|th)[^>]*>\s*(?:` + labels + `)\s*:?\s*</(?:td|th)>\s*)(<(?:td|th)[^>]*>\s*)([^<]+)`)
}

var (
	passwordCellRe = buildCellPattern(passwordLabels)
	codeCellRe     = buildCellPattern(codeLabels)
	secretCellRe   = buildCellPattern(secretLabels)
)

// cellPatternsFor mirrors patternsFor for the two-cell layout.
func cellPatternsFor(l Level) []*regexp.Regexp {
	switch l {
	case Off:
		return nil
	case Passwords:
		return []*regexp.Regexp{passwordCellRe}
	case Codes:
		return []*regexp.Regexp{passwordCellRe, codeCellRe}
	default:
		return []*regexp.Regexp{passwordCellRe, codeCellRe, secretCellRe}
	}
}

// htmlPatternsFor mirrors patternsFor, including its fail-safe default.
func htmlPatternsFor(l Level) []*regexp.Regexp {
	switch l {
	case Off:
		return nil
	case Passwords:
		return []*regexp.Regexp{passwordHTMLRe}
	case Codes:
		return []*regexp.Regexp{passwordHTMLRe, codeHTMLRe}
	case Secrets:
		return []*regexp.Regexp{passwordHTMLRe, codeHTMLRe, secretHTMLRe}
	default:
		return []*regexp.Regexp{passwordHTMLRe, codeHTMLRe, secretHTMLRe}
	}
}

// patternsFor returns the matchers a level turns on. Ordered widest last so a
// body is walked once per family rather than once per label.
func patternsFor(l Level) []*regexp.Regexp {
	switch l {
	case Off:
		return nil
	case Passwords:
		return []*regexp.Regexp{passwordRe}
	case Codes:
		return []*regexp.Regexp{passwordRe, codeRe}
	case Secrets:
		return []*regexp.Regexp{passwordRe, codeRe, secretRe}
	default:
		// An unknown level is a newer setting this build does not understand.
		// Redact rather than expose: a gateway rolled back mid-deploy should
		// not start showing passwords because it read a number from the future.
		return []*regexp.Regexp{passwordRe, codeRe, secretRe}
	}
}

// Text masks secrets in a plain-text body.
func Text(body string, l Level) string {
	if l == Off || body == "" {
		return body
	}
	for _, re := range patternsFor(l) {
		body = re.ReplaceAllString(body, "${1}${2}"+Mask)
	}
	return maskUnlabelled(body, l)
}

// maskUnlabelled runs the matchers that need no label.
//
// Shapes fire from Passwords up rather than only at Secrets: an AWS key or a
// PEM block is issued rather than chosen, so there is no false positive to
// weigh against leaking one. Cards wait for Secrets because Luhn makes them
// plausible, not certain, and a run of digits is something people legitimately
// put in mail.
func maskUnlabelled(s string, l Level) string {
	switch l {
	case Off:
		return s
	case Passwords, Codes:
		return maskShapes(s)
	default:
		// Secrets, and any level this build does not recognise — the same
		// fail-safe as patternsFor. Widest rather than narrowest, because a
		// row written by a newer build must not read as "show everything".
		return maskCards(maskShapes(s))
	}
}

// HTML masks secrets in an HTML body.
//
// The same patterns, and deliberately not an HTML parse. The value capture
// stops at `<`, so a match cannot run past the end of a text node and cannot
// eat a tag — which means the markup a redacted document renders is the markup
// it had. Parsing and re-serialising would be more precise about text nodes and
// would also rewrite every document that went through it, including ones with
// nothing to redact.
//
// The cost is a password split across tags — `<b>pass</b>word: x` — is missed.
// That is the accepted gap: this protects against mail that contains a
// credential, not against mail crafted to smuggle one past the filter, and the
// sender in that scenario already has the password.
func HTML(body string, l Level) string {
	if l == Off || body == "" {
		return body
	}
	for _, re := range htmlPatternsFor(l) {
		body = re.ReplaceAllString(body, "${1}${2}${3}"+Mask)
	}
	// The two-cell layout with no colon: `<td>Password</td><td>hunter2</td>`.
	// The label cell has to hold the label and nothing else, which is what
	// keeps "Change your password" in a heading from taking the cell beside it.
	for _, re := range cellPatternsFor(l) {
		body = re.ReplaceAllString(body, "${1}${2}"+Mask)
	}
	return maskUnlabelled(body, l)
}

// Subject masks secrets in a subject line.
//
// Subjects carry codes more often than bodies do — "Your verification code is
// 481920" is a whole genre — and the delivery list shows them without anyone
// opening a message.
func Subject(subject string, l Level) string {
	if l == Off || subject == "" {
		return subject
	}
	for _, re := range patternsFor(l) {
		subject = re.ReplaceAllString(subject, "${1}${2}"+Mask)
	}
	return maskUnlabelled(subject, l)
}

// LevelFromString maps the stored form to a Level, defaulting to Passwords for
// anything unrecognised — including the empty string, which is a deployment
// that has never opened the setting.
func LevelFromString(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return Off
	case "codes":
		return Codes
	case "secrets":
		return Secrets
	default:
		return Passwords
	}
}

// String renders a Level for storage and for the settings API.
func (l Level) String() string {
	switch l {
	case Off:
		return "off"
	case Codes:
		return "codes"
	case Secrets:
		return "secrets"
	default:
		return "passwords"
	}
}
