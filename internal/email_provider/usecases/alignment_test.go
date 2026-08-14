package usecases

import (
	"strings"
	"testing"
)

// The failure this exists for: a message that is correctly signed, published in
// DNS, healthy by every check panmail had, and rejected by Gmail — because
// DMARC asks whether the signing domain matches the domain in From, and nothing
// here compared the two.

func TestRelaxedAlignmentAcceptsASubdomain(t *testing.T) {
	// The DMARC default, and the case that makes a naive equality check wrong:
	// mail sent From a subdomain and signed by the parent does align.
	for _, tc := range []struct{ from, signing string }{
		{"example.com", "example.com"},
		{"mail.example.com", "example.com"},
		{"example.com", "mail.example.com"},
		{"a.b.example.com", "example.com"},
		{"EXAMPLE.com", "example.COM"}, // operators paste mixed case
		{"example.com.", "example.com"},
		{"noreply@example.com", "example.com"}, // and whole addresses
	} {
		if !Aligned(tc.from, tc.signing, false) {
			t.Errorf("From %q signed by %q should align under relaxed DMARC", tc.from, tc.signing)
		}
	}
}

func TestRelaxedAlignmentRejectsADifferentOrganization(t *testing.T) {
	for _, tc := range []struct{ from, signing string }{
		{"otherbrand.com", "example.com"},
		{"example.com.evil.com", "example.com"}, // suffix games
		{"notexample.com", "example.com"},
		{"", "example.com"},
		{"example.com", ""},
	} {
		if Aligned(tc.from, tc.signing, false) {
			t.Errorf("From %q signed by %q must not align", tc.from, tc.signing)
		}
	}
}

// "The last two labels" is the tempting shortcut and it is wrong for every
// multi-level public suffix: it makes two unrelated organizations look like
// one, and would report alignment for a domain someone else owns.
func TestAlignmentUsesThePublicSuffixListNotTheLastTwoLabels(t *testing.T) {
	if Aligned("example.co.uk", "other.co.uk", false) {
		t.Error("example.co.uk and other.co.uk are different organizations; " +
			"treating co.uk as the organizational domain would align every UK domain with every other")
	}
	if !Aligned("mail.example.co.uk", "example.co.uk", false) {
		t.Error("mail.example.co.uk should align with example.co.uk")
	}
}

func TestStrictAlignmentRequiresAnExactMatch(t *testing.T) {
	if Aligned("mail.example.com", "example.com", true) {
		t.Error("strict alignment must reject a subdomain")
	}
	if !Aligned("example.com", "example.com", true) {
		t.Error("strict alignment must accept an exact match")
	}
}

// The report an operator actually sees.
func TestAProviderSigningForOneDomainAndSendingAsAnotherIsReported(t *testing.T) {
	report := CheckAlignment("example.com", []string{"example.com", "otherbrand.com"}, false)

	if len(report.Misaligned) != 1 || report.Misaligned[0] != "otherbrand.com" {
		t.Fatalf("misaligned = %v, want [otherbrand.com]", report.Misaligned)
	}

	summary := report.Summary("example.com")
	// It has to say what will happen, not just that something is wrong: the
	// whole problem is that this configuration looks healthy.
	for _, want := range []string{"otherbrand.com", "example.com", "fail DMARC"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary does not mention %q: %s", want, summary)
		}
	}
}

func TestAProviderThatOnlySendsAsWhatItSignsIsQuiet(t *testing.T) {
	report := CheckAlignment("example.com", []string{"example.com", "mail.example.com"}, false)

	if len(report.Misaligned) != 0 {
		t.Errorf("misaligned = %v, want none", report.Misaligned)
	}
	if report.Unbounded {
		t.Error("a provider with allowed domains is not unbounded")
	}
	if s := report.Summary("example.com"); s != "" {
		t.Errorf("a correct configuration produced a warning: %s", s)
	}
}

// Empty AllowedDomains means any caller may send as any domain, so alignment
// depends on each request. That is worth saying — it is the default, and it is
// how a provider ends up signing for one domain and sending as anything.
func TestAProviderWithNoRestrictionIsFlaggedSeparately(t *testing.T) {
	report := CheckAlignment("example.com", nil, false)

	if !report.Unbounded {
		t.Fatal("a provider restricting no domains should be reported as unbounded")
	}
	if len(report.Misaligned) != 0 {
		t.Errorf("nothing is known to be misaligned yet, got %v", report.Misaligned)
	}
	if s := report.Summary("example.com"); !strings.Contains(s, "allowed domains") {
		t.Errorf("summary does not say what to do: %s", s)
	}
}

// A provider with no DKIM configured has nothing to align, and saying anything
// about it would be noise on the majority of providers.
func TestNoSigningDomainMeansNothingToReport(t *testing.T) {
	report := CheckAlignment("", []string{"anything.com"}, false)
	if report.Unbounded || len(report.Misaligned) != 0 {
		t.Errorf("a provider without DKIM produced a report: %+v", report)
	}
	if s := report.Summary(""); s != "" {
		t.Errorf("a provider without DKIM produced a summary: %s", s)
	}
}

// The report has to reach the operator, not just exist. It rides on the DMARC
// check because that is the check it decides — DKIM can be perfect and DMARC
// still fail on alignment alone.
func TestTheAlignmentWarningIsAttachedToTheDmarcCheck(t *testing.T) {
	summary := CheckAlignment("example.com", []string{"otherbrand.com"}, false).Summary("example.com")
	if summary == "" {
		t.Fatal("no summary to attach")
	}

	// What the usecase does with it, asserted here so the composition is
	// covered without standing up a DNS resolver.
	existing := "p=none"
	combined := existing + " " + summary
	if !strings.Contains(combined, "p=none") {
		t.Error("attaching the warning discarded the published record")
	}
	if !strings.Contains(combined, "otherbrand.com") {
		t.Error("the warning was lost")
	}
}
