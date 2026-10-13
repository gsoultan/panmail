package emailfilter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

func detects(number int64, detectors ...string) emailfilter.Condition {
	return emailfilter.Condition{
		Field: emailfilter.FieldSensitiveData, Operator: emailfilter.OpDetects,
		Values: detectors, Number: number,
	}
}

func TestDetectorsFindWhatTheyClaim(t *testing.T) {
	cases := map[string]struct {
		detector string
		text     string
		want     bool
	}{
		"card spaced":           {"credit_card", "Kartu saya 4111 1111 1111 1111 ya", true},
		"card fails luhn":       {"credit_card", "Order 4111 1111 1111 1112", false},
		"card dashed":           {"credit_card", "5500-0000-0000-0004", true},
		"nik":                   {"id_nik", "NIK 3201234506900001", true},
		"nik bad province":      {"id_nik", "9901234506900001", false},
		"nik bad month":         {"id_nik", "3201230113900001", false},
		"nik zero serial":       {"id_nik", "3201234506900000", false},
		"nik inside longer run": {"id_nik", "132012345069000012", false},
		"npwp printed":          {"id_npwp", "NPWP kami 01.234.567.8-901.000", true},
		"npwp labelled bare":    {"id_npwp", "NPWP: 012345678901000", true},
		"bare digits unlabeled": {"id_npwp", "ref 012345678901000", false},
		"iban":                  {"iban", "Transfer to GB82 WEST 1234 5698 7654 32", true},
		"iban bad check":        {"iban", "GB82 WEST 1234 5698 7654 33", false},
		"email":                 {"email_address", "kontak budi.santoso@example.co.id", true},
		"no email":              {"email_address", "write to us at example dot com", false},
		"mobile local":          {"phone_number", "WA 0812-3456-7890", true},
		"mobile intl":           {"phone_number", "call +62 812 3456 7890", true},
		"other intl":            {"phone_number", "London +44 20 7946 0958", true},
		"plain number":          {"phone_number", "invoice 20794609", false},
		"aws key":               {"secret", "key AKIAIOSFODNN7EXAMPLE here", true},
		"github token":          {"secret", "ghp_" + strings.Repeat("a1", 18), true},
		"prose":                 {"secret", "the secret is patience", false},
		"private key": {"private_key",
			"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(1, tc.detector))
			m := msg(func(m *emailfilter.Message) { m.Text = tc.text })
			if _, got := r.Matches(m); got != tc.want {
				t.Errorf("%s in %q: matched = %v, want %v", tc.detector, tc.text, got, tc.want)
			}
		})
	}
}

func TestDetectionCountsDistinctValuesAgainstTheThreshold(t *testing.T) {
	body := "a@example.com, b@example.com, c@example.com, and again A@example.com"
	m := msg(func(m *emailfilter.Message) { m.Text = body })

	// Three distinct addresses: the repeat of a@ in another case is one.
	if _, ok := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(3, "email_address")).Matches(m); !ok {
		t.Error("three distinct addresses against a threshold of three: want a match")
	}
	if _, ok := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(4, "email_address")).Matches(m); ok {
		t.Error("three distinct addresses against a threshold of four: want no match")
	}
}

func TestDetectionSumsAcrossDetectors(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.Text = "Kartu 4111 1111 1111 1111, NIK 3201234506900001"
	})
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(2, "credit_card", "id_nik"))
	if _, ok := r.Matches(m); !ok {
		t.Error("one card and one NIK against a threshold of two between them: want a match")
	}
}

func TestOneNumberWrittenTwoWaysCountsOnce(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.Text = "Card 4111111111111111, or as printed 4111 1111 1111 1111. WA 0812-3456-7890 / +62 812 3456 7890"
	})
	d := emailfilter.Evaluate([]emailfilter.Rule{
		rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(1, "credit_card", "phone_number")),
	}, emailfilter.DirectionOutbound, m)
	if d.Action != emailfilter.ActionHold {
		t.Fatalf("action = %q, want hold", d.Action)
	}
	for _, f := range d.Matched[0].Findings {
		if f.Count != 1 {
			t.Errorf("%s: count = %d, want 1", f.Detector, f.Count)
		}
	}
}

func TestDetectionLooksEverywhereAndSaysWhere(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.Subject = "Data gaji"
		m.Text = ""
		// Split by markup and spaced with &nbsp;: still one card to a reader.
		m.HTML = `<p>4111 <span>1111</span> 1111&nbsp;1111</p><style>.x{}</style>`
		m.Attachments = []emailfilter.Attachment{
			{Filename: "payroll.xlsx", Text: "Budi 5500 0000 0000 0004\nSiti 4012 8888 8888 1881"},
		}
	})
	d := emailfilter.Evaluate([]emailfilter.Rule{
		rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(3, "credit_card")),
	}, emailfilter.DirectionOutbound, m)
	if d.Action != emailfilter.ActionHold {
		t.Fatalf("three cards across the body and an attachment: action = %q, want hold", d.Action)
	}
	if len(d.Matched) != 1 || len(d.Matched[0].Findings) != 1 {
		t.Fatalf("matched = %+v, want one condition with one finding", d.Matched)
	}
	f := d.Matched[0].Findings[0]
	if f.Detector != emailfilter.DetectCreditCard || f.Count != 3 {
		t.Errorf("finding = %s ×%d, want credit_card ×3", f.Detector, f.Count)
	}
	if got := strings.Join(f.Locations, ","); got != "body,attachment:payroll.xlsx" {
		t.Errorf("locations = %q", got)
	}
	// The audit trail must never become the leak: last four only.
	for _, s := range f.Samples {
		if strings.Count(s, "*") != 12 || len(s) != 16 {
			t.Errorf("sample %q is not masked to the last four", s)
		}
	}
	if f.Samples[0] != "************1111" {
		t.Errorf("first sample = %q", f.Samples[0])
	}
}

func TestSamplesAreMasked(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.Text = "AKIAIOSFODNN7EXAMPLE budi@example.co.id NIK 3201234506900001\n" +
			"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----"
	})
	d := emailfilter.Evaluate([]emailfilter.Rule{
		rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
			detects(1, "secret", "email_address", "id_nik", "private_key")),
	}, emailfilter.DirectionOutbound, m)
	want := map[emailfilter.Detector]string{
		emailfilter.DetectSecret:       "AKIA********",
		emailfilter.DetectEmailAddress: "b***@example.co.id",
		emailfilter.DetectNIK:          "************0001",
		emailfilter.DetectPrivateKey:   "-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	if len(d.Matched) != 1 || len(d.Matched[0].Findings) != len(want) {
		t.Fatalf("matched = %+v", d.Matched)
	}
	for _, f := range d.Matched[0].Findings {
		if len(f.Samples) != 1 || f.Samples[0] != want[f.Detector] {
			t.Errorf("%s samples = %q, want [%q]", f.Detector, f.Samples, want[f.Detector])
		}
	}
}

func TestSensitiveDataAsAnException(t *testing.T) {
	// A detector works as an exception too: tag mail to partners, except
	// mail carrying a key, which a stricter rule below can hold.
	r := emailfilter.Rule{
		Name: "partners without keys", Direction: emailfilter.DirectionOutbound,
		Action: emailfilter.ActionTag, Enabled: true,
		Conditions: []emailfilter.Condition{cond(emailfilter.FieldTo, emailfilter.OpDomainIs, "partner.net")},
		Exceptions: []emailfilter.Condition{detects(1, "secret")},
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	clean := msg(nil)
	leaky := msg(func(m *emailfilter.Message) { m.Text = "AKIAIOSFODNN7EXAMPLE" })
	if _, ok := r.Matches(clean); !ok {
		t.Error("clean mail to a partner: want the rule to fire")
	}
	if _, ok := r.Matches(leaky); ok {
		t.Error("a key in the body is the exception: want the rule not to fire")
	}
}

func TestSensitiveDataValidation(t *testing.T) {
	bad := []emailfilter.Condition{
		detects(1),                     // no detector
		detects(1, "credit_card", "x"), // unknown detector
		detects(-1, "credit_card"),     // negative threshold
		{Field: emailfilter.FieldSensitiveData, Operator: emailfilter.OpContains, Values: []string{"credit_card"}},
	}
	for i, c := range bad {
		r := emailfilter.Rule{Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Enabled: true, Conditions: []emailfilter.Condition{c}}
		if err := r.Validate(); err == nil {
			t.Errorf("case %d (%+v): want a validation error", i, c)
		}
	}

	// Findings are output. A client that sends them on a rule has them dropped.
	c := detects(0, "credit_card")
	c.Findings = []emailfilter.Finding{{Detector: "credit_card", Count: 99}}
	r := rule(t, emailfilter.DirectionInbound, emailfilter.ActionHold, c)
	if len(r.Conditions[0].Findings) != 0 {
		t.Error("Validate kept client-supplied findings")
	}
	// Zero is one: a rule that names a detector means "any".
	if _, ok := r.Matches(msg(func(m *emailfilter.Message) { m.Text = "4111111111111111" })); !ok {
		t.Error("threshold zero with one card: want a match")
	}
}

func TestScreenReadsAttachmentsForSensitiveData(t *testing.T) {
	// No attachment_text condition anywhere: the detector alone has to be
	// enough to make Screen open the file.
	hold := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(1, "id_nik"))
	s := emailfilter.NewScreener(staticRules{rules: []emailfilter.Rule{hold}}, newFakeQuarantine(), 0, nil)

	data := wordFile(t, "Nama: Siti, NIK 3201234506900001")
	m := msg(func(m *emailfilter.Message) {
		m.Attachments = []emailfilter.Attachment{{Filename: "ktp.docx", Size: int64(len(data)), Data: data}}
	})
	d, err := s.Screen(context.Background(), "tenant", emailfilter.DirectionOutbound, m)
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if d.Action != emailfilter.ActionHold {
		t.Fatalf("a NIK inside a Word file: action = %q, want hold", d.Action)
	}
	if loc := d.Matched[0].Findings[0].Locations; len(loc) != 1 || loc[0] != "attachment:ktp.docx" {
		t.Errorf("locations = %q, want [attachment:ktp.docx]", loc)
	}
}

func TestRulesWithoutDetectorsAreUnchanged(t *testing.T) {
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldSubject, emailfilter.OpContains, "quarterly"))
	d := emailfilter.Evaluate([]emailfilter.Rule{r}, emailfilter.DirectionOutbound, msg(nil))
	if d.Action != emailfilter.ActionHold || len(d.Matched) != 1 || d.Matched[0].Findings != nil {
		t.Errorf("decision = %+v, want a hold with no findings", d)
	}
}

// A webhook subscriber triages from the held event alone, so the findings
// travel with it, masked as they are in the queue.
func TestTheHeldEventCarriesFindings(t *testing.T) {
	notifier := &countingNotifier{}
	s := emailfilter.NewScreener(nil, newFakeQuarantine(), 0, notifier)

	m := msg(func(m *emailfilter.Message) { m.Text = "4111 1111 1111 1111" })
	d := emailfilter.Evaluate([]emailfilter.Rule{
		rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, detects(1, "credit_card")),
	}, emailfilter.DirectionOutbound, m)
	record := emailfilter.NewFilteredMessage("t1", emailfilter.DirectionOutbound, m, d)
	if err := s.Record(context.Background(), &record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("notified %d times, want 1", len(notifier.events))
	}
	matched := notifier.events[0].Matched
	if len(matched) != 1 || len(matched[0].Findings) != 1 || matched[0].Findings[0].Samples[0] != "************1111" {
		t.Errorf("matched = %+v, want the masked card finding", matched)
	}
}
