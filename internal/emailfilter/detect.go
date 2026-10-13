package emailfilter

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/gsoultan/panmail/internal/redact"
)

// Detector names a built-in recogniser for a kind of sensitive data.
//
// A pattern a tenant writes by hand finds sixteen digits; a detector finds a
// card number, which is sixteen digits that also pass the checksum every
// issuer uses. That difference is the whole reason these exist: a DLP rule on
// "matches \d{16}" holds every order confirmation, and a rule that holds
// everything is a rule somebody turns off.
type Detector string

const (
	// DetectCreditCard is a 13 to 19 digit run that passes Luhn.
	DetectCreditCard Detector = "credit_card"

	// DetectNIK is an Indonesian Nomor Induk Kependudukan: sixteen digits whose
	// province, birth date and serial all have to be plausible.
	DetectNIK Detector = "id_nik"

	// DetectNPWP is an Indonesian tax number, in its printed
	// 00.000.000.0-000.000 form or as bare digits right after the word NPWP.
	DetectNPWP Detector = "id_npwp"

	// DetectIBAN is an international bank account number that passes mod 97.
	DetectIBAN Detector = "iban"

	// DetectEmailAddress is any address in the content, as opposed to the
	// envelope. Worth a threshold: one is a signature, two hundred is a
	// customer list.
	DetectEmailAddress Detector = "email_address"

	// DetectPhoneNumber is an Indonesian mobile number, or any international
	// number written with a leading +.
	DetectPhoneNumber Detector = "phone_number"

	// DetectSecret is an issued credential: cloud, Git hosting, payment, chat
	// and model API keys, and JWTs. The same shapes the redactor masks.
	DetectSecret Detector = "secret"

	// DetectPrivateKey is a PEM private key block.
	DetectPrivateKey Detector = "private_key"
)

// detectorFinders is the catalogue. Each returns the raw matches in one piece
// of text; counting, de-duplication and masking happen once, in scan.
var detectorFinders = map[Detector]func(string) []string{
	DetectCreditCard:   redact.FindCards,
	DetectNIK:          findNIK,
	DetectNPWP:         findNPWP,
	DetectIBAN:         findIBAN,
	DetectEmailAddress: findEmailAddresses,
	DetectPhoneNumber:  findPhoneNumbers,
	DetectSecret:       redact.FindSecrets,
	DetectPrivateKey:   redact.FindPrivateKeys,
}

// Detectors lists every built-in detector, sorted, for validation messages
// and for anything that wants to offer them.
func Detectors() []Detector {
	out := make([]Detector, 0, len(detectorFinders))
	for d := range detectorFinders {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (d Detector) Valid() bool {
	_, ok := detectorFinders[d]
	return ok
}

// Finding is what a detector saw, in a form safe to keep.
//
// It is the audit trail for a sensitive-data match: a reviewer needs to know
// that the message held three card numbers in the body and one in
// payroll.xlsx, and does not need the numbers themselves. Samples are masked
// before they are stored, so a quarantine row never becomes the leak it was
// written to stop.
type Finding struct {
	Detector Detector `json:"detector"`

	// Count is distinct values, not occurrences. The same card quoted three
	// times down a reply chain is one card, and a threshold of "five NIKs"
	// means five people.
	Count int `json:"count"`

	// Locations is where they were found: subject, body, attachment_name:<file>
	// or attachment:<file>.
	Locations []string `json:"locations,omitempty"`

	// Samples is up to maxSamples matches, masked.
	Samples []string `json:"samples,omitempty"`
}

const maxSamples = 3

// maxScanBytes bounds how much of any one piece of text is scanned. RE2 is
// linear, so this is about the send path's latency on a pathological body
// rather than about safety. A message that hides its card numbers past the
// fourth megabyte of one part is not the case this is tuned for.
const maxScanBytes = 4 << 20

// scanTarget is one piece of text and where it came from.
type scanTarget struct {
	location string
	text     string
}

// scanTargets is everything a detector looks at.
//
// The HTML part is reduced to text first: a card number split across
// <span>s, or spaced with &nbsp;, is still a card number to whoever reads it.
func scanTargets(m Message) []scanTarget {
	targets := []scanTarget{
		{"subject", m.Subject},
		{"body", m.Text},
		{"body", htmlToText(m.HTML)},
	}
	for _, a := range m.Attachments {
		targets = append(targets, scanTarget{"attachment_name:" + a.Filename, a.Filename})
	}
	for _, a := range m.Attachments {
		// Text is filled in by Screen when a rule needs it; see
		// needsAttachmentText. An attachment nobody could read stays empty.
		targets = append(targets, scanTarget{"attachment:" + a.Filename, a.Text})
	}
	return targets
}

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(?:script|style)>`)
	tagRe         = regexp.MustCompile(`<[^>]*>`)
)

func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	s = scriptStyleRe.ReplaceAllString(s, " ")
	// Inline tags join what they split, so a number broken by a <span> is
	// whole again; block-level breaks are rare inside one value and a space
	// would cost more matches than it saves.
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.ReplaceAll(s, " ", " ")
}

// detectionCache remembers what each detector found in one message, so a
// rule set with several sensitive-data conditions scans the content once per
// detector rather than once per condition.
//
// Lazy: the targets, and the HTML stripping they involve, are built on the
// first detector asked for, so a rule set with no sensitive-data condition
// pays nothing.
type detectionCache struct {
	message Message
	targets []scanTarget
	built   bool
	results map[Detector]Finding
}

func newDetectionCache(m Message) *detectionCache {
	return &detectionCache{message: m, results: map[Detector]Finding{}}
}

func (c *detectionCache) find(d Detector) Finding {
	if f, ok := c.results[d]; ok {
		return f
	}
	if !c.built {
		c.targets = scanTargets(c.message)
		c.built = true
	}
	f := scan(d, c.targets)
	c.results[d] = f
	return f
}

// scan runs one detector over every target.
func scan(d Detector, targets []scanTarget) Finding {
	finder := detectorFinders[d]
	finding := Finding{Detector: d}
	if finder == nil {
		return finding
	}
	seen := map[string]bool{}
	located := map[string]bool{}
	for _, t := range targets {
		text := t.text
		if text == "" {
			continue
		}
		if len(text) > maxScanBytes {
			text = text[:maxScanBytes]
		}
		for _, match := range finder(text) {
			key := normaliseMatch(d, match)
			if !located[t.location] {
				located[t.location] = true
				finding.Locations = append(finding.Locations, t.location)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			finding.Count++
			if len(finding.Samples) < maxSamples {
				finding.Samples = append(finding.Samples, maskSample(d, match))
			}
		}
	}
	return finding
}

// normaliseMatch is the identity of a match for counting, so the same card
// written with and without spaces counts once.
func normaliseMatch(d Detector, match string) string {
	switch d {
	case DetectEmailAddress, DetectSecret, DetectPrivateKey:
		return strings.ToLower(match)
	case DetectPhoneNumber:
		// 0812..., 62812... and +62812... are one number.
		digits := alnum(match)
		if strings.HasPrefix(digits, "628") {
			return "0" + digits[2:]
		}
		return digits
	}
	return alnum(match)
}

func alnum(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return strings.ToUpper(b.String())
}

// maskSample keeps just enough of a match for a reviewer to recognise it: the
// last four of a number, the domain of an address, the issuer prefix of a key.
func maskSample(d Detector, match string) string {
	switch d {
	case DetectEmailAddress:
		at := strings.LastIndex(match, "@")
		if at <= 0 {
			return "***"
		}
		return match[:1] + "***" + match[at:]
	case DetectPrivateKey:
		if end := strings.Index(match, "-----\n"); end > 0 {
			return match[:end+5]
		}
		if line, _, ok := strings.Cut(match, "\n"); ok {
			return strings.TrimSpace(line)
		}
		return "-----BEGIN PRIVATE KEY-----"
	case DetectSecret:
		if len(match) <= 4 {
			return "****"
		}
		return match[:4] + strings.Repeat("*", 8)
	}
	digits := alnum(match)
	if len(digits) <= 4 {
		return strings.Repeat("*", len(digits))
	}
	return strings.Repeat("*", len(digits)-4) + digits[len(digits)-4:]
}

// --- Indonesian identity numbers -------------------------------------------

var nikRe = regexp.MustCompile(`\b\d{16}\b`)

// nikProvinces are the two-digit province codes Dukcapil issues, including the
// Papua provinces created in 2022. A sixteen-digit run that does not start
// with one is not a NIK.
var nikProvinces = map[string]bool{
	"11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true, "18": true, "19": true,
	"21": true,
	"31": true, "32": true, "33": true, "34": true, "35": true, "36": true,
	"51": true, "52": true, "53": true,
	"61": true, "62": true, "63": true, "64": true, "65": true,
	"71": true, "72": true, "73": true, "74": true, "75": true, "76": true,
	"81": true, "82": true,
	"91": true, "92": true, "93": true, "94": true, "95": true, "96": true, "97": true,
}

func findNIK(s string) []string {
	var out []string
	for _, candidate := range nikRe.FindAllString(s, -1) {
		if validNIK(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// validNIK checks the structure: PP KK CC DDMMYY SSSS. Women's day of birth
// has forty added, so the day is 01-31 or 41-71. A NIK has no checksum, which
// is why every other part is held to its range: the structure is the only
// thing separating one from a sixteen-digit order number.
func validNIK(s string) bool {
	if len(s) != 16 || !nikProvinces[s[0:2]] {
		return false
	}
	if s[2:4] == "00" || s[4:6] == "00" {
		return false
	}
	day := atoi2(s[6:8])
	if day > 40 {
		day -= 40
	}
	month := atoi2(s[8:10])
	if day < 1 || day > 31 || month < 1 || month > 12 {
		return false
	}
	return s[12:16] != "0000"
}

func atoi2(s string) int {
	return int(s[0]-'0')*10 + int(s[1]-'0')
}

var (
	npwpPrintedRe = regexp.MustCompile(`\b\d{2}\.\d{3}\.\d{3}\.\d-\d{3}\.\d{3}\b`)

	// Bare digits are only an NPWP when they are introduced as one. Fifteen or
	// sixteen digits alone are too common to claim.
	npwpLabelledRe = regexp.MustCompile(`(?i)\bnpwp\b[^0-9\r\n]{0,20}(\d{15,16})\b`)
)

func findNPWP(s string) []string {
	out := npwpPrintedRe.FindAllString(s, -1)
	for _, m := range npwpLabelledRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// --- Bank accounts ---------------------------------------------------------

var ibanRe = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?\b`)

func findIBAN(s string) []string {
	var out []string
	for _, candidate := range ibanRe.FindAllString(s, -1) {
		if validIBAN(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// validIBAN is the ISO 13616 check: move the first four characters to the
// end, turn letters into numbers, and the result mod 97 is 1.
func validIBAN(s string) bool {
	s = alnum(s)
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	rearranged := s[4:] + s[:4]
	remainder := 0
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			remainder = (remainder*10 + int(r-'0')) % 97
		case r >= 'A' && r <= 'Z':
			remainder = (remainder*100 + int(r-'A'+10)) % 97
		default:
			return false
		}
	}
	return remainder == 1
}

// --- Contact details -------------------------------------------------------

var emailAddressRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)

func findEmailAddresses(s string) []string {
	return emailAddressRe.FindAllString(s, -1)
}

var (
	// An Indonesian mobile number: 08, 628 or +628, then eight to eleven more
	// digits, grouped however the writer liked.
	idMobileRe = regexp.MustCompile(`(?:\+62|\b62|\b0)[ .-]?8\d(?:[ .-]?\d){7,10}\b`)

	// Anything else written in international form. The + is required: without
	// it there is no telling a phone number from any other long number.
	intlPhoneRe = regexp.MustCompile(`\+[1-9](?:[ .-]?\(?\d\)?){7,14}\b`)
)

func findPhoneNumbers(s string) []string {
	out := idMobileRe.FindAllString(s, -1)
	for _, m := range intlPhoneRe.FindAllString(s, -1) {
		// +62 8... is already found by the stricter pattern.
		if strings.HasPrefix(alnum(m), "628") {
			continue
		}
		out = append(out, m)
	}
	return out
}

// validateDetectors checks the values of a sensitive-data condition.
func validateDetectors(values []string) error {
	for _, v := range values {
		if !Detector(strings.TrimSpace(v)).Valid() {
			return fmt.Errorf("emailfilter: %q is not a detector (want one of %v)", v, Detectors())
		}
	}
	return nil
}
