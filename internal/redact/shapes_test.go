package redact_test

import (
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/redact"
)

// The gap the label matcher could never close: a credential nobody introduced.
func TestIssuedShapesAreCaughtWithoutALabel(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"AWS access key", "AKIAIOSFODNN7EXAMPLE"},
		{"GitHub token", "ghp_" + strings.Repeat("a", 36)},
		{"GitHub fine-grained", "github_pat_" + strings.Repeat("b", 30)},
		{"Stripe live key", "sk_live_" + strings.Repeat("c", 24)},
		// Assembled rather than written out. GitHub's own push protection
		// rejected this file when the token was a single literal, which is a
		// fair verdict — a fixture that looks enough like a live Slack token to
		// block a push is one somebody will eventually mistake for a real
		// credential. Splitting it keeps the shape the matcher needs without
		// putting a scanner-shaped string in the source.
		{"Slack bot token", "xoxb" + "-" + "123456789012" + "-" + strings.Repeat("z", 16)},
		{"Google API key", "AIza" + strings.Repeat("d", 35)},
		{"OpenAI key", "sk-proj-" + strings.Repeat("e", 32)},
		{"SendGrid key", "SG." + strings.Repeat("f", 22) + "." + strings.Repeat("g", 22)},
		{"JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No label anywhere — just the secret in a sentence.
			body := "Here you go: " + tc.secret + " — let me know if it works."
			got := redact.Text(body, redact.Passwords)

			if strings.Contains(got, tc.secret) {
				t.Errorf("%s survived at the default level: %q", tc.name, got)
			}
			if !strings.Contains(got, "let me know if it works.") {
				t.Errorf("the rest of the message was eaten: %q", got)
			}
		})
	}
}

// A private key block keeps its header, so an operator can see what the message
// carried and that redaction fired.
func TestPrivateKeyBlocksKeepTheirHeader(t *testing.T) {
	body := `Attached:
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAx7Vv9mVJ3mJ0nQmVYq0bZ8kL
qWmZ0cV1nT8sP2dR5fH3jK9xB4nM6wY7uE1aG0iO
-----END RSA PRIVATE KEY-----
Regards.`
	got := redact.Text(body, redact.Passwords)

	if strings.Contains(got, "MIIEowIBAAKCAQEA") {
		t.Errorf("the key body survived: %q", got)
	}
	if !strings.Contains(got, "-----BEGIN RSA PRIVATE KEY-----") ||
		!strings.Contains(got, "-----END RSA PRIVATE KEY-----") {
		t.Errorf("the header or footer was lost: %q", got)
	}
	if !strings.Contains(got, "Regards.") {
		t.Errorf("content after the block was eaten: %q", got)
	}
}

// The safety case for matching without a label. These all look secret-ish and
// none of them are; a redactor that eats them is one people switch off.
func TestOrdinaryContentIsNotMistakenForASecret(t *testing.T) {
	for _, in := range []string{
		"Your order 4820-1122-9931 has shipped.",
		"Tracking: 1Z999AA10123456784",
		"Call us on +44 20 7946 0958.",
		"See https://example.com/a/b?utm_source=newsletter&utm_campaign=spring",
		"Message-ID: <20260904.abcdefghijklmnop@example.com>",
		"The invoice total is 1234567890123.",
		"skip_the_queue_with_this_link",
		"Ask for Sarah in accounts.",
	} {
		if got := redact.Text(in, redact.Secrets); got != in {
			t.Errorf("Text(%q) = %q, want it untouched", in, got)
		}
	}
}

func TestCardNumbersNeedTheChecksumAndTheLevel(t *testing.T) {
	// A real test card number, Luhn-valid.
	const card = "4242 4242 4242 4242"

	t.Run("masked at secrets", func(t *testing.T) {
		got := redact.Text("Charged to "+card+" today.", redact.Secrets)
		if strings.Contains(got, "4242 4242 4242 4242") {
			t.Errorf("a valid card survived: %q", got)
		}
		if !strings.Contains(got, "today.") {
			t.Errorf("the rest of the line was eaten: %q", got)
		}
	})

	t.Run("left alone below secrets", func(t *testing.T) {
		in := "Charged to " + card + " today."
		if got := redact.Text(in, redact.Passwords); got != in {
			t.Errorf("cards masked at the default level: %q", got)
		}
	})

	t.Run("a number that fails Luhn is not a card", func(t *testing.T) {
		in := "Reference 4242 4242 4242 4243 for your records."
		if got := redact.Text(in, redact.Secrets); got != in {
			t.Errorf("a Luhn-invalid run was masked: %q", got)
		}
	})

	t.Run("too short to be a card", func(t *testing.T) {
		// Passes Luhn but is only eight digits.
		in := "PIN group 4242 4242 here."
		if got := redact.Text(in, redact.Secrets); got != in {
			t.Errorf("a short run was masked as a card: %q", got)
		}
	})
}

// The other half of how HTML mail writes a credential, and the half the
// delimiter-anchored pattern cannot see because there is no delimiter.
func TestTheTwoCellTableLayoutIsCaught(t *testing.T) {
	in := `<table><tr><td>Password</td><td>hunter2</td></tr><tr><td>Order</td><td>A-1</td></tr></table>`
	got := redact.HTML(in, redact.Passwords)

	if strings.Contains(got, "hunter2") {
		t.Errorf("the password survived: %q", got)
	}
	if !strings.Contains(got, "A-1") {
		t.Errorf("the unrelated row was eaten: %q", got)
	}
	for _, tag := range []string{"<table>", "</table>", "<tr>", "</td>"} {
		if !strings.Contains(got, tag) {
			t.Errorf("%s was lost: %q", tag, got)
		}
	}
}

// The restriction that makes the cell pattern safe: the label cell has to hold
// the label and nothing else, so a sentence does not take the cell beside it.
func TestASentenceInACellDoesNotTakeTheNextOne(t *testing.T) {
	in := `<table><tr><td>You can change your password here</td><td>Account settings</td></tr></table>`
	got := redact.HTML(in, redact.Passwords)

	if !strings.Contains(got, "Account settings") {
		t.Errorf("a prose cell took the cell beside it: %q", got)
	}
}

// Shapes are found in HTML too, and must not disturb the markup around them.
func TestShapesAreFoundInHTML(t *testing.T) {
	in := `<p>Use <code>AKIAIOSFODNN7EXAMPLE</code> to authenticate.</p>`
	got := redact.HTML(in, redact.Passwords)

	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("the key survived: %q", got)
	}
	if !strings.Contains(got, "<code>") || !strings.Contains(got, "</p>") {
		t.Errorf("markup was damaged: %q", got)
	}
}

// Off means off, for the new matchers as much as the old ones.
func TestOffLeavesShapesAndCardsAlone(t *testing.T) {
	in := "key AKIAIOSFODNN7EXAMPLE and card 4242 4242 4242 4242"
	if got := redact.Text(in, redact.Off); got != in {
		t.Errorf("Off redacted something: %q", got)
	}
}
