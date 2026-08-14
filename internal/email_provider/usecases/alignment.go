package usecases

import (
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// DMARC alignment: whether a valid signature will actually help.
//
// A signature proves the message came from whoever controls the domain in its
// d= tag. DMARC asks a further question the signature cannot answer on its own
// — is that the same domain the recipient sees in From? — and a message where
// the two differ fails DMARC however good the signature is.
//
// This is the gap between the two things panmail already checks. The provider's
// AllowedDomains decides which From addresses it will accept; the provider's
// DKIM configuration decides what the signature claims. Nothing compared them,
// so a provider signing for one domain while sending as another produced mail
// that was correctly signed, published in DNS, healthy by every check here, and
// rejected by Gmail.
//
// Aligned reports whether a From domain aligns with a signing domain.
//
// Relaxed alignment is the DMARC default and what this implements when strict
// is false: the two must share an organizational domain, so mail.example.com
// signed by example.com aligns. Strict requires them to be identical.
//
// The organizational domain comes from the public suffix list rather than
// "the last two labels", because that rule is wrong for every multi-level
// suffix: it makes example.co.uk and other.co.uk look like the same
// organization, which would report alignment for a domain someone else owns.
func Aligned(fromDomain, signingDomain string, strict bool) bool {
	from := normalizeDomain(fromDomain)
	signing := normalizeDomain(signingDomain)
	if from == "" || signing == "" {
		return false
	}
	if from == signing {
		return true
	}
	if strict {
		return false
	}

	fromOrg, err := publicsuffix.EffectiveTLDPlusOne(from)
	if err != nil {
		return false
	}
	signingOrg, err := publicsuffix.EffectiveTLDPlusOne(signing)
	if err != nil {
		return false
	}
	return fromOrg == signingOrg
}

// AlignmentReport describes what a provider's configuration will do to DMARC.
type AlignmentReport struct {
	// Misaligned lists the allowed From domains that do not align with the
	// signing domain. Empty means every domain this provider may send as will
	// pass DMARC on the DKIM side.
	Misaligned []string

	// Unbounded is set when the provider restricts no From domains at all, so
	// alignment depends entirely on what each caller asks to send as.
	Unbounded bool
}

// Summary is the sentence an operator reads. Empty when there is nothing to say.
func (r AlignmentReport) Summary(signingDomain string) string {
	switch {
	case len(r.Misaligned) > 0:
		return fmt.Sprintf(
			"DKIM signs as %s, but this provider may also send From %s. "+
				"Those messages will be signed correctly and still fail DMARC, because DMARC "+
				"requires the From domain to align with the signing domain. Sign with a key for "+
				"each sending domain, or restrict this provider to %s.",
			signingDomain, strings.Join(r.Misaligned, ", "), signingDomain)
	case r.Unbounded:
		return fmt.Sprintf(
			"DKIM signs as %s and this provider restricts no From domains, so any caller "+
				"sending as another domain will fail DMARC despite a valid signature. "+
				"Set allowed domains to the domains you sign for.",
			signingDomain)
	default:
		return ""
	}
}

// CheckAlignment compares a provider's signing domain against the From domains
// it is permitted to send as.
func CheckAlignment(signingDomain string, allowedDomains []string, strict bool) AlignmentReport {
	if normalizeDomain(signingDomain) == "" {
		return AlignmentReport{}
	}
	if len(allowedDomains) == 0 {
		return AlignmentReport{Unbounded: true}
	}

	var report AlignmentReport
	for _, domain := range allowedDomains {
		if normalizeDomain(domain) == "" {
			continue
		}
		if !Aligned(domain, signingDomain, strict) {
			report.Misaligned = append(report.Misaligned, normalizeDomain(domain))
		}
	}
	return report
}

// normalizeDomain reduces a configured value to something comparable. Operators
// paste addresses, trailing dots and mixed case into these fields.
func normalizeDomain(value string) string {
	d := strings.ToLower(strings.TrimSpace(value))
	if at := strings.LastIndex(d, "@"); at >= 0 {
		d = d[at+1:]
	}
	return strings.Trim(d, ".")
}
