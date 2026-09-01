// Package emailfilter decides what happens to a message before it is sent or
// after it arrives.
//
// A rule is a list of conditions that must all hold, a list of exceptions that
// must all not hold, and an action. That is the shape Exchange transport rules
// and Sieve settled on, and it covers nearly every real rule without a nested
// boolean expression — which matters because someone has to build a form for
// this, and a form for an expression tree is a form nobody uses.
//
// Nothing here does I/O. Evaluate takes a message and a set of rules and
// returns a decision; the callers in internal/email and internal/inbound are
// what read the rules and act on the answer.
package emailfilter

import (
	"path/filepath"
	"strings"
)

// Direction is which pipeline a rule applies to. A rule is written for one or
// the other: "from address is outside the company" means opposite things on
// the way in and the way out, so a rule that ran on both would be a trap.
type Direction string

const (
	DirectionOutbound Direction = "outbound"
	DirectionInbound  Direction = "inbound"
)

func (d Direction) Valid() bool {
	return d == DirectionOutbound || d == DirectionInbound
}

// Attachment is what a rule can ask about a file, without holding its bytes.
// Size rather than content: the evaluator runs on the send path, and reading a
// 20 MB attachment to answer "is it bigger than 10 MB" would put the whole
// thing through memory for a question the header already answers.
type Attachment struct {
	Filename    string
	ContentType string
	Size        int64
}

// Extension is the lowercased extension with no dot, or "" if there is none.
func (a Attachment) Extension() string {
	ext := strings.ToLower(filepath.Ext(a.Filename))
	return strings.TrimPrefix(ext, ".")
}

// executableExtensions are the ones worth naming as a category, because
// "block executables" is a rule people want and spelling out thirty
// extensions by hand is how one gets missed.
//
// Extension-based, and deliberately not claiming to be more: renaming
// evil.exe to evil.txt defeats it. It is a policy control, not a scanner.
var executableExtensions = map[string]bool{
	"exe": true, "com": true, "bat": true, "cmd": true, "msi": true,
	"scr": true, "pif": true, "cpl": true, "jar": true, "js": true,
	"jse": true, "vbs": true, "vbe": true, "wsf": true, "wsh": true,
	"ps1": true, "psm1": true, "sh": true, "bash": true, "app": true,
	"dmg": true, "pkg": true, "deb": true, "rpm": true, "apk": true,
	"dll": true, "so": true, "reg": true, "lnk": true, "hta": true,
	"chm": true, "iso": true, "img": true, "vhd": true,
}

// IsExecutable reports whether the extension is one that can run.
func (a Attachment) IsExecutable() bool {
	return executableExtensions[a.Extension()]
}

// AuthResult is an inbound authentication verdict. Empty means the check did
// not run, which is not the same as failing and must not be treated as such.
type AuthResult string

const (
	AuthPass    AuthResult = "pass"
	AuthFail    AuthResult = "fail"
	AuthNeutral AuthResult = "neutral"
	AuthNone    AuthResult = "none"
)

// Message is the view of a mail that rules are written against. Both
// pipelines project onto it, so one rule engine serves an outbound
// SendEmailRequest and an inbound parsed MIME message without either
// leaking into the other.
type Message struct {
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	HTML    string
	Text    string

	// Headers is canonicalised to lowercase keys by NewMessage. A header may
	// legitimately appear more than once — Received is the obvious one — so
	// the value is a list.
	Headers map[string][]string

	Attachments []Attachment

	// Size is the whole message including attachments, in bytes.
	Size int64

	// ProviderID is set outbound only; inbound has no provider yet.
	ProviderID string

	// SPF, DKIM and DMARC are set inbound only.
	SPF   AuthResult
	DKIM  AuthResult
	DMARC AuthResult
}

// Recipients is every address that receives a copy, blind or not, once.
// Rules about "any recipient" mean exactly this list.
func (m Message) Recipients() []string {
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	seen := make(map[string]bool, cap(out))
	for _, list := range [][]string{m.To, m.Cc, m.Bcc} {
		for _, address := range list {
			key := strings.ToLower(strings.TrimSpace(address))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, address)
		}
	}
	return out
}

// Header returns the values of a header, matched case-insensitively.
func (m Message) Header(name string) []string {
	return m.Headers[strings.ToLower(strings.TrimSpace(name))]
}

// NormaliseHeaders lowercases the keys of a header map, which is what Message
// expects. Callers building a Message from a parsed MIME message should run
// their header map through this rather than trusting the parser's casing.
func NormaliseHeaders(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, values := range in {
		key := strings.ToLower(strings.TrimSpace(name))
		out[key] = append(out[key], values...)
	}
	return out
}

// TotalAttachmentSize is the sum of every attachment.
func (m Message) TotalAttachmentSize() int64 {
	var total int64
	for _, a := range m.Attachments {
		total += a.Size
	}
	return total
}

// LargestAttachment is the size of the biggest one, or zero when there are none.
func (m Message) LargestAttachment() int64 {
	var largest int64
	for _, a := range m.Attachments {
		if a.Size > largest {
			largest = a.Size
		}
	}
	return largest
}
