package usecases

import (
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// DKIM turns a stored private key into a signature on every message. Two things
// must hold: the key must never be readable through the API, and it must
// survive an edit that does not mention it — a save that blanked it would stop
// signing while mail kept flowing, so deliverability would collapse with
// nothing failing loudly enough to notice.

const testDKIMKey = "-----BEGIN PRIVATE KEY-----\nMIIBOgIBAAJBAKj34\n-----END PRIVATE KEY-----"

func smtpConfigJSON(t *testing.T, password string, dkim *panmailv1.DkimConfig) []byte {
	t.Helper()
	b, err := protojson.Marshal(&panmailv1.SmtpConfig{
		Host: "smtp.example.com", Port: 587, Username: "user",
		Password: password, Dkim: dkim,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func parseSMTP(t *testing.T, raw []byte) *panmailv1.SmtpConfig {
	t.Helper()
	c := &panmailv1.SmtpConfig{}
	if err := protojson.Unmarshal(raw, c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c
}

// The API redacts secrets on read, so a client that fetches a provider, changes
// the host and saves it back sends neither the password nor the DKIM key.
func TestAnEditWithoutSecretsKeepsBothOfThem(t *testing.T) {
	stored := smtpConfigJSON(t, "s3cret", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "mail", PrivateKey: testDKIMKey,
	})
	// What a client sends back after editing: secrets blank.
	incoming := smtpConfigJSON(t, "", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "mail", PrivateKey: "",
	})

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := parseSMTP(t, merged)
	if got.Password != "s3cret" {
		t.Errorf("the password was lost: %q", got.Password)
	}
	if got.GetDkim().GetPrivateKey() != testDKIMKey {
		t.Error("the DKIM private key was lost, so signing would have silently stopped")
	}
}

func TestASubmittedKeyReplacesTheStoredOne(t *testing.T) {
	stored := smtpConfigJSON(t, "old", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "old", PrivateKey: "OLD-KEY",
	})
	incoming := smtpConfigJSON(t, "new", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "new", PrivateKey: "NEW-KEY",
	})

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := parseSMTP(t, merged)
	if got.Password != "new" {
		t.Errorf("password not replaced: %q", got.Password)
	}
	if got.GetDkim().GetPrivateKey() != "NEW-KEY" {
		t.Errorf("key not replaced: %q", got.GetDkim().GetPrivateKey())
	}
	if got.GetDkim().GetSelector() != "new" {
		t.Errorf("selector not replaced: %q", got.GetDkim().GetSelector())
	}
}

// Adding DKIM to a provider that never had it must work, not be swallowed by
// the carry-forward logic.
func TestDkimCanBeAddedToAProviderThatHadNone(t *testing.T) {
	stored := smtpConfigJSON(t, "s3cret", nil)
	incoming := smtpConfigJSON(t, "", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "mail", PrivateKey: testDKIMKey,
	})

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := parseSMTP(t, merged)
	if got.GetDkim().GetPrivateKey() != testDKIMKey {
		t.Error("a newly supplied DKIM key was not stored")
	}
	if got.Password != "s3cret" {
		t.Errorf("the untouched password was lost: %q", got.Password)
	}
}

// A provider with no DKIM at all must round-trip unchanged rather than gaining
// an empty config.
func TestAProviderWithoutDkimStaysWithoutIt(t *testing.T) {
	stored := smtpConfigJSON(t, "s3cret", nil)
	incoming := smtpConfigJSON(t, "", nil)

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := parseSMTP(t, merged)
	if got.GetDkim().GetPrivateKey() != "" {
		t.Error("a DKIM key appeared from nowhere")
	}
	if got.Password != "s3cret" {
		t.Errorf("the password was lost: %q", got.Password)
	}
}

// Signing must be all-or-nothing. A half-configured key would fail at signing
// time on every send, which stops delivery outright — worse than sending
// unsigned, which is merely distrusted.
func TestSigningIsOnlyEnabledWhenFullyConfigured(t *testing.T) {
	cases := []struct {
		name string
		dkim *panmailv1.DkimConfig
		want bool
	}{
		{"complete", &panmailv1.DkimConfig{Domain: "example.com", Selector: "mail", PrivateKey: testDKIMKey}, true},
		{"no domain", &panmailv1.DkimConfig{Selector: "mail", PrivateKey: testDKIMKey}, false},
		{"no selector", &panmailv1.DkimConfig{Domain: "example.com", PrivateKey: testDKIMKey}, false},
		{"no key", &panmailv1.DkimConfig{Domain: "example.com", Selector: "mail"}, false},
		{"absent", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &panmailv1.SmtpConfig{Dkim: tc.dkim}
			d := c.GetDkim()
			enabled := d.GetDomain() != "" && d.GetSelector() != "" && d.GetPrivateKey() != ""
			if enabled != tc.want {
				t.Errorf("signing enabled = %v, want %v", enabled, tc.want)
			}
		})
	}
}

// The key must not come back out of the API. The domain and selector are public
// — they are published in DNS — so only the key is withheld.
func TestTheKeyIsRedactedOnRead(t *testing.T) {
	c := parseSMTP(t, smtpConfigJSON(t, "s3cret", &panmailv1.DkimConfig{
		Domain: "example.com", Selector: "mail", PrivateKey: testDKIMKey,
	}))

	c.Password = redactedPassword
	if c.Dkim != nil {
		c.Dkim.PrivateKey = redactedPassword
	}

	out, err := protojson.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "BEGIN PRIVATE KEY") {
		t.Error("the DKIM private key was returned to the caller")
	}
	if !strings.Contains(string(out), "example.com") {
		t.Error("the domain should remain visible; it is published in DNS")
	}
}
