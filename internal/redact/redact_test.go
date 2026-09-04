package redact_test

import (
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/redact"
)

func TestTheValueGoesAndTheLabelStays(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"colon", "Password: hunter2", "Password: *****"},
		{"equals", "password=hunter2", "password=*****"},
		{"is", "Your password is hunter2", "Your password is *****"},
		{"passphrase", "Passphrase: correct horse battery", "Passphrase: *****"},
		{"pwd", "pwd: s3cret", "pwd: *****"},
		{"uppercase label", "PASSWORD: s3cret", "PASSWORD: *****"},
		{"indonesian", "Kata sandi: rahasia", "Kata sandi: *****"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact.Text(tc.in, redact.Passwords); got != tc.want {
				t.Errorf("Text(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The label alone must not fire. A message about password policy is the common
// case, and a redactor that eats it makes the whole feature something operators
// turn off.
func TestProseAboutPasswordsSurvives(t *testing.T) {
	for _, in := range []string{
		"Please choose a strong password before you sign in.",
		"We never ask for your password by email.",
		"Your password has been changed successfully.",
		"password", // a bare label with no value
	} {
		if got := redact.Text(in, redact.Passwords); got != in {
			t.Errorf("Text(%q) = %q, want it untouched", in, got)
		}
	}
}

// The value stops at the end of the line. Swallowing the rest of the message
// would destroy the record this archive exists to keep.
func TestRedactionStopsAtTheEndOfTheLine(t *testing.T) {
	in := "Password: hunter2\nYour order ships Tuesday.\nThanks!"
	got := redact.Text(in, redact.Passwords)

	if !strings.Contains(got, "Your order ships Tuesday.") {
		t.Errorf("the rest of the message was eaten: %q", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("the password survived: %q", got)
	}
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Errorf("line count changed to %d, want 2", lines)
	}
}

// The capture stops at `<`, so a match inside a table cell cannot run past the
// closing tag and take the markup with it.
func TestHTMLKeepsItsMarkup(t *testing.T) {
	in := `<table><tr><td>Password:</td><td>hunter2</td></tr><tr><td>Order</td><td>A-1</td></tr></table>`
	got := redact.HTML(in, redact.Passwords)

	if strings.Contains(got, "hunter2") {
		t.Errorf("the password survived: %q", got)
	}
	for _, tag := range []string{"<table>", "</table>", "<tr>", "</td>"} {
		if !strings.Contains(got, tag) {
			t.Errorf("%s was lost, markup is broken: %q", tag, got)
		}
	}
	if !strings.Contains(got, "A-1") {
		t.Errorf("unrelated content was eaten: %q", got)
	}
}

func TestHTMLValueInline(t *testing.T) {
	in := `<p>Your temporary password: hunter2</p><p>See you soon.</p>`
	got := redact.HTML(in, redact.Passwords)

	if strings.Contains(got, "hunter2") {
		t.Errorf("the password survived: %q", got)
	}
	if !strings.Contains(got, "See you soon.") {
		t.Errorf("the following paragraph was eaten: %q", got)
	}
}

// Each level turns on exactly what it says and nothing wider.
func TestLevelsAreCumulativeAndBounded(t *testing.T) {
	body := "Password: hunter2\nVerification code: 481920\nAPI key: sk_live_abc123"

	t.Run("off changes nothing", func(t *testing.T) {
		if got := redact.Text(body, redact.Off); got != body {
			t.Errorf("Off redacted something: %q", got)
		}
	})

	t.Run("passwords leaves codes and keys", func(t *testing.T) {
		got := redact.Text(body, redact.Passwords)
		if strings.Contains(got, "hunter2") {
			t.Error("the password survived")
		}
		if !strings.Contains(got, "481920") || !strings.Contains(got, "sk_live_abc123") {
			t.Errorf("Passwords redacted more than passwords: %q", got)
		}
	})

	t.Run("codes leaves keys", func(t *testing.T) {
		got := redact.Text(body, redact.Codes)
		if strings.Contains(got, "hunter2") || strings.Contains(got, "481920") {
			t.Errorf("password or code survived: %q", got)
		}
		if !strings.Contains(got, "sk_live_abc123") {
			t.Errorf("Codes redacted an API key: %q", got)
		}
	})

	t.Run("secrets takes all three", func(t *testing.T) {
		got := redact.Text(body, redact.Secrets)
		for _, secret := range []string{"hunter2", "481920", "sk_live_abc123"} {
			if strings.Contains(got, secret) {
				t.Errorf("%s survived at Secrets: %q", secret, got)
			}
		}
	})
}

// The zero value has to be safe. A caller that forgets to set the level gets
// redaction, not exposure.
func TestTheZeroValueRedacts(t *testing.T) {
	var l redact.Level
	if l != redact.Passwords {
		t.Fatalf("zero Level = %v, want Passwords", l)
	}
	if got := redact.Text("Password: hunter2", l); strings.Contains(got, "hunter2") {
		t.Errorf("the zero value exposed a password: %q", got)
	}
}

// A level this build does not recognise is a newer setting, which happens
// during a rolled-back deploy. It must not read as Off.
func TestAnUnknownLevelRedactsRatherThanExposes(t *testing.T) {
	future := redact.Level(99)
	got := redact.Text("Password: hunter2", future)
	if strings.Contains(got, "hunter2") {
		t.Errorf("an unknown level exposed a password: %q", got)
	}
}

func TestLevelStringRoundTrip(t *testing.T) {
	for _, l := range []redact.Level{redact.Off, redact.Passwords, redact.Codes, redact.Secrets} {
		if got := redact.LevelFromString(l.String()); got != l {
			t.Errorf("round trip of %v gave %v", l, got)
		}
	}
	// Anything unrecognised, including a deployment that never set it.
	if got := redact.LevelFromString(""); got != redact.Passwords {
		t.Errorf("empty string = %v, want Passwords", got)
	}
	if got := redact.LevelFromString("nonsense"); got != redact.Passwords {
		t.Errorf("unrecognised = %v, want Passwords", got)
	}
}

// The mask is fixed width. Matching the original length would leak it, and
// knowing a password was eight characters is a real head start.
func TestTheMaskDoesNotLeakTheLength(t *testing.T) {
	short := redact.Text("password: ab", redact.Passwords)
	long := redact.Text("password: abcdefghijklmnopqrstuvwxyz", redact.Passwords)
	if short != long {
		t.Errorf("mask width varies with the secret: %q vs %q", short, long)
	}
}

func TestSubjectsAreRedactedToo(t *testing.T) {
	// The delivery list shows subjects without anyone opening a message.
	got := redact.Subject("Your verification code is 481920", redact.Codes)
	if strings.Contains(got, "481920") {
		t.Errorf("a code survived in the subject: %q", got)
	}
}
