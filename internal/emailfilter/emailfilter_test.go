package emailfilter_test

import (
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

func msg(over func(*emailfilter.Message)) emailfilter.Message {
	m := emailfilter.Message{
		From:    "alice@example.com",
		To:      []string{"bob@partner.net"},
		Subject: "Quarterly report",
		Text:    "Attached, as promised.",
		Size:    2048,
		Headers: emailfilter.NormaliseHeaders(map[string][]string{
			"X-Mailer": {"panmail"},
		}),
	}
	if over != nil {
		over(&m)
	}
	return m
}

// rule builds a validated rule. Validation is what compiles patterns, so a
// test that skipped it would silently never match a regex condition.
func rule(t *testing.T, direction emailfilter.Direction, action emailfilter.Action, conditions ...emailfilter.Condition) emailfilter.Rule {
	t.Helper()
	r := emailfilter.Rule{
		Name: "test", Direction: direction, Action: action,
		Enabled: true, Conditions: conditions,
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return r
}

func cond(field emailfilter.Field, op emailfilter.Operator, values ...string) emailfilter.Condition {
	return emailfilter.Condition{Field: field, Operator: op, Values: values}
}

func TestConditionsAcrossEveryField(t *testing.T) {
	withAttachments := func(m *emailfilter.Message) {
		m.Attachments = []emailfilter.Attachment{
			{Filename: "report.pdf", ContentType: "application/pdf", Size: 900},
			{Filename: "macro.xlsm", ContentType: "application/vnd.ms-excel", Size: 4096},
		}
	}

	cases := map[string]struct {
		condition emailfilter.Condition
		message   emailfilter.Message
		want      bool
	}{
		"from contains":        {cond(emailfilter.FieldFrom, emailfilter.OpContains, "alice"), msg(nil), true},
		"from contains misses": {cond(emailfilter.FieldFrom, emailfilter.OpContains, "carol"), msg(nil), false},
		"from equals":          {cond(emailfilter.FieldFrom, emailfilter.OpEquals, "ALICE@EXAMPLE.COM"), msg(nil), true},
		"from domain":          {cond(emailfilter.FieldFrom, emailfilter.OpDomainIs, "example.com"), msg(nil), true},
		"from matches":         {cond(emailfilter.FieldFrom, emailfilter.OpMatches, `^al.ce@`), msg(nil), true},
		"to domain":            {cond(emailfilter.FieldTo, emailfilter.OpDomainIs, "partner.net"), msg(nil), true},
		"subject contains":     {cond(emailfilter.FieldSubject, emailfilter.OpContains, "quarterly"), msg(nil), true},
		"body contains":        {cond(emailfilter.FieldBody, emailfilter.OpContains, "promised"), msg(nil), true},
		"subject or body":      {cond(emailfilter.FieldSubjectOrBody, emailfilter.OpContains, "promised"), msg(nil), true},
		"message size over":    {emailfilter.Condition{Field: emailfilter.FieldMessageSize, Operator: emailfilter.OpGreaterOrEqual, Number: 1024}, msg(nil), true},
		"message size under":   {emailfilter.Condition{Field: emailfilter.FieldMessageSize, Operator: emailfilter.OpGreaterOrEqual, Number: 4096}, msg(nil), false},
		"recipient count":      {emailfilter.Condition{Field: emailfilter.FieldRecipientCount, Operator: emailfilter.OpGreaterOrEqual, Number: 1}, msg(nil), true},
		"header exists":        {emailfilter.Condition{Field: emailfilter.FieldHeader, Operator: emailfilter.OpExists, Header: "x-mailer"}, msg(nil), true},
		"header absent":        {emailfilter.Condition{Field: emailfilter.FieldHeader, Operator: emailfilter.OpExists, Header: "x-spam"}, msg(nil), false},
		"header contains":      {emailfilter.Condition{Field: emailfilter.FieldHeader, Operator: emailfilter.OpContains, Header: "X-MAILER", Values: []string{"panmail"}}, msg(nil), true},

		"has attachment":        {emailfilter.Condition{Field: emailfilter.FieldHasAttachment, Operator: emailfilter.OpIsTrue}, msg(withAttachments), true},
		"has attachment none":   {emailfilter.Condition{Field: emailfilter.FieldHasAttachment, Operator: emailfilter.OpIsTrue}, msg(nil), false},
		"attachment count":      {emailfilter.Condition{Field: emailfilter.FieldAttachmentCount, Operator: emailfilter.OpGreaterOrEqual, Number: 2}, msg(withAttachments), true},
		"attachment name":       {cond(emailfilter.FieldAttachmentName, emailfilter.OpContains, "report"), msg(withAttachments), true},
		"attachment extension":  {cond(emailfilter.FieldAttachmentExtension, emailfilter.OpIn, "xlsm", "docm"), msg(withAttachments), true},
		"largest attachment":    {emailfilter.Condition{Field: emailfilter.FieldAttachmentSize, Operator: emailfilter.OpGreaterOrEqual, Number: 4096}, msg(withAttachments), true},
		"total attachment size": {emailfilter.Condition{Field: emailfilter.FieldTotalAttachmentSize, Operator: emailfilter.OpGreaterOrEqual, Number: 4996}, msg(withAttachments), true},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold, c.condition)
			if _, got := r.Matches(c.message); got != c.want {
				t.Errorf("matched = %v, want %v", got, c.want)
			}
		})
	}
}

// Renaming evil.exe to evil.txt defeats this, and it is meant to: it is a
// policy control over what people are allowed to send, not a scanner.
func TestExecutableAttachmentsAreRecognisedByExtension(t *testing.T) {
	for _, name := range []string{"setup.exe", "run.BAT", "installer.msi", "payload.js", "archive.jar", "script.ps1"} {
		m := msg(func(m *emailfilter.Message) {
			m.Attachments = []emailfilter.Attachment{{Filename: name, Size: 10}}
		})
		r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionReject,
			emailfilter.Condition{Field: emailfilter.FieldHasExecutableAttachment, Operator: emailfilter.OpIsTrue})
		if _, ok := r.Matches(m); !ok {
			t.Errorf("%s was not recognised as executable", name)
		}
	}

	safe := msg(func(m *emailfilter.Message) {
		m.Attachments = []emailfilter.Attachment{{Filename: "report.pdf", Size: 10}}
	})
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionReject,
		emailfilter.Condition{Field: emailfilter.FieldHasExecutableAttachment, Operator: emailfilter.OpIsTrue})
	if _, ok := r.Matches(safe); ok {
		t.Error("a pdf was treated as executable")
	}
}

// Without the dot, a rule about example.com would also catch notexample.com,
// which is exactly the domain an attacker registers.
func TestDomainMatchingCoversSubdomainsButNotSuffixes(t *testing.T) {
	cases := map[string]bool{
		"alice@example.com":      true,
		"alice@mail.example.com": true,
		"alice@notexample.com":   false,
		"alice@example.com.evil": false,
	}
	for address, want := range cases {
		m := msg(func(m *emailfilter.Message) { m.From = address })
		r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
			cond(emailfilter.FieldFrom, emailfilter.OpDomainIs, "example.com"))
		if _, got := r.Matches(m); got != want {
			t.Errorf("%s matched = %v, want %v", address, got, want)
		}
	}
}

// A blind recipient is still a recipient. A rule about who receives a message
// that skipped Bcc would be trivially evaded.
func TestAnyRecipientIncludesBcc(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.To = []string{"bob@partner.net"}
		m.Bcc = []string{"leak@competitor.example"}
	})
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldAnyRecipient, emailfilter.OpDomainIs, "competitor.example"))
	if _, ok := r.Matches(m); !ok {
		t.Error("a Bcc recipient did not match an any-recipient rule")
	}
}

func TestRecipientsAreDeduplicated(t *testing.T) {
	m := msg(func(m *emailfilter.Message) {
		m.To = []string{"bob@partner.net"}
		m.Cc = []string{"BOB@PARTNER.NET"}
	})
	if got := len(m.Recipients()); got != 1 {
		t.Errorf("Recipients() = %d, want 1", got)
	}
}

func TestConditionsAreAndedAndExceptionsSubtract(t *testing.T) {
	r := emailfilter.Rule{
		Name: "big pdf to partners", Direction: emailfilter.DirectionOutbound,
		Action: emailfilter.ActionHold, Enabled: true,
		Conditions: []emailfilter.Condition{
			cond(emailfilter.FieldAnyRecipient, emailfilter.OpDomainIs, "partner.net"),
			{Field: emailfilter.FieldHasAttachment, Operator: emailfilter.OpIsTrue},
		},
		Exceptions: []emailfilter.Condition{
			cond(emailfilter.FieldFrom, emailfilter.OpEquals, "trusted@example.com"),
		},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	withFile := func(from string) emailfilter.Message {
		return msg(func(m *emailfilter.Message) {
			m.From = from
			m.Attachments = []emailfilter.Attachment{{Filename: "a.pdf", Size: 1}}
		})
	}

	if _, ok := r.Matches(withFile("alice@example.com")); !ok {
		t.Error("both conditions held but the rule did not fire")
	}
	if _, ok := r.Matches(withFile("trusted@example.com")); ok {
		t.Error("the exception did not suppress the rule")
	}
	// Only one condition holds: no attachment.
	if _, ok := r.Matches(msg(nil)); ok {
		t.Error("the rule fired with only one of two conditions met")
	}
}

func TestEvaluateTakesTheLowestPriorityMatchAndAllowShortCircuits(t *testing.T) {
	hold := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldAnyRecipient, emailfilter.OpDomainIs, "partner.net"))
	hold.Priority = 10

	allow := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionAllow,
		cond(emailfilter.FieldFrom, emailfilter.OpEquals, "alice@example.com"))
	allow.Priority = 1

	rules := []emailfilter.Rule{hold, allow}

	d := emailfilter.Evaluate(rules, emailfilter.DirectionOutbound, msg(nil))
	if d.Action != "" {
		t.Errorf("action = %q, want the allow rule to short-circuit", d.Action)
	}
	if !d.Delivers() {
		t.Error("an allow decision did not deliver")
	}
	if d.Rule == nil || d.Rule.Action != emailfilter.ActionAllow {
		t.Error("the deciding rule was not reported")
	}

	// Someone the allow rule does not cover still gets held.
	other := msg(func(m *emailfilter.Message) { m.From = "mallory@example.com" })
	if d := emailfilter.Evaluate(rules, emailfilter.DirectionOutbound, other); d.Action != emailfilter.ActionHold {
		t.Errorf("action = %q, want hold", d.Action)
	}
}

func TestEvaluateIgnoresTheOtherDirectionAndDisabledRules(t *testing.T) {
	inbound := rule(t, emailfilter.DirectionInbound, emailfilter.ActionHold,
		cond(emailfilter.FieldFrom, emailfilter.OpContains, "alice"))
	disabled := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldFrom, emailfilter.OpContains, "alice"))
	disabled.Enabled = false

	if d := emailfilter.Evaluate([]emailfilter.Rule{inbound, disabled}, emailfilter.DirectionOutbound, msg(nil)); d.Action != "" {
		t.Errorf("action = %q, want nothing to match", d.Action)
	}
}

// Filtering that is not configured must not be filtering that blocks.
func TestNoRulesDelivers(t *testing.T) {
	d := emailfilter.Evaluate(nil, emailfilter.DirectionOutbound, msg(nil))
	if !d.Delivers() || d.Action != "" || d.Rule != nil {
		t.Errorf("empty rule set produced %+v, want a delivering zero decision", d)
	}
}

func TestDecisionDelivers(t *testing.T) {
	for action, want := range map[emailfilter.Action]bool{
		"": true, emailfilter.ActionAllow: true, emailfilter.ActionTag: true,
		emailfilter.ActionHold: false, emailfilter.ActionReject: false,
	} {
		if got := (emailfilter.Decision{Action: action}).Delivers(); got != want {
			t.Errorf("%q delivers = %v, want %v", action, got, want)
		}
	}
}

func TestMatchedConditionsAreReportedForReview(t *testing.T) {
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldSubject, emailfilter.OpContains, "quarterly"),
		cond(emailfilter.FieldFrom, emailfilter.OpDomainIs, "example.com"))

	d := emailfilter.Evaluate([]emailfilter.Rule{r}, emailfilter.DirectionOutbound, msg(nil))
	if len(d.Matched) != 2 {
		t.Fatalf("Matched = %d conditions, want both", len(d.Matched))
	}
}

func TestValidateRejectsBadRules(t *testing.T) {
	cases := map[string]emailfilter.Rule{
		"no name": {Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldFrom, emailfilter.OpContains, "a")}},
		"no direction": {Name: "x", Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldFrom, emailfilter.OpContains, "a")}},
		"no action": {Name: "x", Direction: emailfilter.DirectionOutbound,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldFrom, emailfilter.OpContains, "a")}},
		"no conditions": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold},
		"unknown field": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond("nonsense", emailfilter.OpContains, "a")}},
		"operator not legal for field": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldMessageSize, emailfilter.OpContains, "a")}},
		"header without a name": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{emailfilter.Condition{Field: emailfilter.FieldHeader, Operator: emailfilter.OpExists}}},
		"no values": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{emailfilter.Condition{Field: emailfilter.FieldFrom, Operator: emailfilter.OpContains}}},
		"bad pattern": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldFrom, emailfilter.OpMatches, "([a-z")}},
		"spf outbound": {Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldSPF, emailfilter.OpEquals, "fail")}},
		"provider inbound": {Name: "x", Direction: emailfilter.DirectionInbound, Action: emailfilter.ActionHold,
			Conditions: []emailfilter.Condition{cond(emailfilter.FieldProvider, emailfilter.OpEquals, "x")}},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if err := r.Validate(); err == nil {
				t.Fatal("Validate accepted a rule it should have refused")
			}
		})
	}
}

// A pattern condition only matches once Validate has compiled it. This is the
// test that fails if someone adds a path that stores a rule without validating.
func TestPatternsOnlyMatchAfterValidation(t *testing.T) {
	unvalidated := emailfilter.Rule{
		Name: "x", Direction: emailfilter.DirectionOutbound, Action: emailfilter.ActionHold,
		Enabled:    true,
		Conditions: []emailfilter.Condition{cond(emailfilter.FieldFrom, emailfilter.OpMatches, "alice")},
	}
	if _, ok := unvalidated.Matches(msg(nil)); ok {
		t.Error("an uncompiled pattern matched, which hides a missing Validate call")
	}

	if err := unvalidated.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, ok := unvalidated.Matches(msg(nil)); !ok {
		t.Error("the pattern did not match after validation")
	}
}

// RE2 has no catastrophic backtracking, which is the reason a tenant may be
// trusted with a regular expression on the send path at all.
func TestAPathologicalPatternDoesNotHang(t *testing.T) {
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldSubject, emailfilter.OpMatches, `(a+)+$`))
	m := msg(func(m *emailfilter.Message) { m.Subject = strings.Repeat("a", 4096) + "!" })

	done := make(chan bool, 1)
	go func() {
		_, ok := r.Matches(m)
		done <- ok
	}()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("matching did not finish")
	}
}
