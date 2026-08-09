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

// SMTP now carries four secrets: the password, the DKIM private key, and the
// OAuth client secret and refresh token. Every one has to survive an edit that
// does not resend it — the DKIM case already proved that forgetting one blanks
// it silently, and OAuth fails differently but just as quietly: the next token
// refresh is rejected and every send stops authenticating.
func TestAllFourSmtpSecretsSurviveAnEditThatOmitsThem(t *testing.T) {
	stored, err := protojson.Marshal(&panmailv1.SmtpConfig{
		Host: "smtp.example.com", Port: 587, Username: "user", Password: "s3cret",
		Dkim: &panmailv1.DkimConfig{Domain: "example.com", Selector: "mail", PrivateKey: testDKIMKey},
		Oauth2: &panmailv1.OAuth2Config{
			Mechanism: "XOAUTH2", ClientId: "cid", ClientSecret: "csecret",
			RefreshToken: "rtoken", TokenEndpoint: "https://oauth2.googleapis.com/token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// What a client sends back after editing only the host: every secret blank,
	// because reads redact all of them.
	incoming, err := protojson.Marshal(&panmailv1.SmtpConfig{
		Host: "smtp2.example.com", Port: 587, Username: "user", Password: "",
		Dkim: &panmailv1.DkimConfig{Domain: "example.com", Selector: "mail", PrivateKey: ""},
		Oauth2: &panmailv1.OAuth2Config{
			Mechanism: "XOAUTH2", ClientId: "cid", ClientSecret: "",
			RefreshToken: "", TokenEndpoint: "https://oauth2.googleapis.com/token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := parseSMTP(t, merged)
	if got.Password != "s3cret" {
		t.Errorf("password lost: %q", got.Password)
	}
	if got.GetDkim().GetPrivateKey() != testDKIMKey {
		t.Error("DKIM private key lost; signing would have stopped silently")
	}
	if got.GetOauth2().GetClientSecret() != "csecret" {
		t.Error("OAuth client secret lost; the next token refresh would be rejected")
	}
	if got.GetOauth2().GetRefreshToken() != "rtoken" {
		t.Error("OAuth refresh token lost; the provider would need reauthorising")
	}
	// The edit itself must still apply.
	if got.Host != "smtp2.example.com" {
		t.Errorf("the edited host was not kept: %q", got.Host)
	}
}

// A newly supplied OAuth secret must replace the stored one.
func TestSubmittedOauthSecretsReplaceTheStoredOnes(t *testing.T) {
	stored, _ := protojson.Marshal(&panmailv1.SmtpConfig{
		Oauth2: &panmailv1.OAuth2Config{ClientSecret: "old", RefreshToken: "old-r"},
	})
	incoming, _ := protojson.Marshal(&panmailv1.SmtpConfig{
		Oauth2: &panmailv1.OAuth2Config{ClientSecret: "new", RefreshToken: "new-r"},
	})

	merged, err := preserveStoredPassword(panmailv1.ProviderType_PROVIDER_TYPE_SMTP, stored, incoming)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := parseSMTP(t, merged)
	if got.GetOauth2().GetClientSecret() != "new" || got.GetOauth2().GetRefreshToken() != "new-r" {
		t.Errorf("secrets not replaced: %+v", got.GetOauth2())
	}
}

// Neither OAuth secret may come back out of the API; the client id, endpoint and
// mechanism must stay visible so the provider can be edited.
func TestOauthSecretsAreRedactedOnRead(t *testing.T) {
	c := &panmailv1.SmtpConfig{
		Oauth2: &panmailv1.OAuth2Config{
			Mechanism: "XOAUTH2", ClientId: "cid", ClientSecret: "csecret",
			RefreshToken: "rtoken", TokenEndpoint: "https://oauth2.googleapis.com/token",
		},
	}
	c.Oauth2.ClientSecret = redactedPassword
	c.Oauth2.RefreshToken = redactedPassword

	out, err := protojson.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "csecret") || strings.Contains(string(out), "rtoken") {
		t.Error("an OAuth credential was returned to the caller")
	}
	if !strings.Contains(string(out), "cid") {
		t.Error("the client id should stay visible; it is not secret and is needed to edit")
	}
}
