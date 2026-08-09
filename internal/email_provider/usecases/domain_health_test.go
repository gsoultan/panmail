package usecases

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

// The check these exercise is one DNS cannot make on its own: whether the key
// published for a selector is the key being signed with. A record can be
// present, parse cleanly and carry a well-formed key from an entirely
// different pair — which is what a rotation with a forgotten DNS update looks
// like — and every signature then fails verification while the page reads
// green.

// --- fake resolver -----------------------------------------------------------

type fakeResolver struct {
	txt map[string][]string
	mx  map[string][]*net.MX
	// err, when set for a name, stands in for a resolver that is broken rather
	// than a name that does not exist. The two must not read the same.
	err map[string]error
}

func (f fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if err, ok := f.err[name]; ok {
		return nil, err
	}
	if records, ok := f.txt[name]; ok {
		return records, nil
	}
	return nil, notFound(name)
}

func (f fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if err, ok := f.err[name]; ok {
		return nil, err
	}
	if records, ok := f.mx[name]; ok {
		return records, nil
	}
	return nil, notFound(name)
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// --- key fixtures ------------------------------------------------------------

// Generating RSA keys is slow enough to notice, and every test wants the same
// two: the one the provider signs with, and an unrelated one standing in for
// whatever is actually published.
var keys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	a, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	b, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return [2]*rsa.PrivateKey{a, b}
})

func signingKey() *rsa.PrivateKey { return keys()[0] }
func otherKey() *rsa.PrivateKey   { return keys()[1] }

func pkcs1PEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// dkimRecord builds the TXT record that publishing `key` would produce.
func dkimRecord(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
}

// --- fixtures ----------------------------------------------------------------

const testTenant = "11111111-1111-1111-1111-111111111111"

func smtpProviderWithDkim(t *testing.T, id, domain, selector, privateKey string) *entities.EmailProvider {
	t.Helper()
	config := &panmailv1.SmtpConfig{
		Host: "smtp.example.com",
		Port: 587,
		Dkim: &panmailv1.DkimConfig{Domain: domain, Selector: selector, PrivateKey: privateKey},
	}
	raw, err := protojson.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return &entities.EmailProvider{
		ID:       id,
		TenantID: testTenant,
		Name:     "Primary",
		Type:     panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		Config:   raw,
	}
}

const providerID = "22222222-2222-2222-2222-222222222222"

func newHealthUsecase(providers map[string]*entities.EmailProvider, resolver fakeResolver) DomainHealthUsecase {
	return NewDomainHealthUsecaseWithResolver(&mockRepo{providers: providers}, resolver, 5*time.Second)
}

func check(t *testing.T, u DomainHealthUsecase, req *panmailv1.CheckDomainHealthRequest) *panmailv1.CheckDomainHealthResponse {
	t.Helper()
	res, err := u.Check(context.Background(), testTenant, req)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return res
}

func dkimFor(t *testing.T, res *panmailv1.CheckDomainHealthResponse, selector string) *panmailv1.DkimSelectorCheck {
	t.Helper()
	for _, d := range res.GetDkim() {
		if d.GetSelector() == selector {
			return d
		}
	}
	t.Fatalf("no DKIM result for selector %q", selector)
	return nil
}

// --- the key comparison ------------------------------------------------------

func TestPublishedKeyMatchesTheSigningKey(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{txt: map[string][]string{
			"s1._domainkey.example.com": {dkimRecord(t, signingKey())},
		}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")

	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MATCHES {
		t.Errorf("key match = %v, want MATCHES (details: %s)", got.GetKeyMatch(), got.GetKeyMatchDetails())
	}
	if !got.GetDns().GetValid() {
		t.Error("the DNS check should also be valid")
	}
}

// The whole reason this feature exists: DNS says everything is fine, and every
// message sent will still fail verification.
func TestAPublishedKeyFromADifferentPairIsAMismatch(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{txt: map[string][]string{
			"s1._domainkey.example.com": {dkimRecord(t, otherKey())},
		}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")

	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MISMATCH {
		t.Fatalf("key match = %v, want MISMATCH", got.GetKeyMatch())
	}
	// The DNS record itself is perfectly well formed, which is exactly why the
	// mismatch needs its own field rather than folding into `valid`.
	if !got.GetDns().GetValid() {
		t.Error("the record parses, so the DNS check should read valid")
	}
	if got.GetKeyMatchDetails() == "" {
		t.Error("a mismatch has to explain itself; it is not self-evident from the record")
	}
}

func TestAPkcs8KeyIsComparedToo(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs8PEM(t, signingKey())),
		},
		fakeResolver{txt: map[string][]string{
			"s1._domainkey.example.com": {dkimRecord(t, signingKey())},
		}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")
	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MATCHES {
		t.Errorf("key match = %v, want MATCHES — gsmail signs with PKCS#8 keys, so they must compare too", got.GetKeyMatch())
	}
}

// A long key is published as several quoted strings that resolvers join, so the
// base64 can come back with whitespace in it. Comparing naively reports every
// 2048-bit key as a mismatch.
func TestWhitespaceInThePublishedKeyIsTolerated(t *testing.T) {
	record := dkimRecord(t, signingKey())
	split := record[:60] + " \n\t" + record[60:]

	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{txt: map[string][]string{"s1._domainkey.example.com": {split}}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")
	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MATCHES {
		t.Errorf("key match = %v, want MATCHES; a wrapped record is still the same key", got.GetKeyMatch())
	}
}

func TestARevokedKeyIsNotReportedAsAMismatch(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{txt: map[string][]string{"s1._domainkey.example.com": {"v=DKIM1; k=rsa; p="}}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")
	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNKNOWN {
		t.Errorf("key match = %v, want UNKNOWN; a revocation is already reported by the DNS check", got.GetKeyMatch())
	}
}

func TestAnUnreadablePrivateKeyIsReportedRatherThanGuessed(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", "not a PEM key at all"),
		},
		fakeResolver{txt: map[string][]string{"s1._domainkey.example.com": {dkimRecord(t, signingKey())}}},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")
	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNKNOWN {
		t.Errorf("key match = %v, want UNKNOWN — an unreadable key is not evidence of a mismatch", got.GetKeyMatch())
	}
	if !strings.Contains(got.GetKeyMatchDetails(), "could not be read") {
		t.Errorf("details = %q, want an explanation of why no comparison happened", got.GetKeyMatchDetails())
	}
}

// Claiming a mismatch on top of a missing record is two complaints about one
// problem, and the second one is misleading — nothing is published to mismatch.
func TestAMissingSelectorIsNotAlsoAMismatch(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{},
	)

	got := dkimFor(t, check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID}), "s1")

	if got.GetDns().GetFound() {
		t.Error("nothing is published, so the record should not read as found")
	}
	if got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNSPECIFIED {
		t.Errorf("key match = %v, want UNSPECIFIED when there is no record to compare", got.GetKeyMatch())
	}
}

// --- what gets checked -------------------------------------------------------

func TestTheProvidersDkimDomainIsPreferredOverTheRequest(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "signing.example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{},
	)

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{
		ProviderId: providerID,
		Domain:     "ignored.example.com",
	})

	if res.GetDomain() != "signing.example.com" {
		t.Errorf("checked %q, want the domain the provider actually signs for", res.GetDomain())
	}
}

func TestAProviderWithoutDkimFallsBackToItsAllowedDomain(t *testing.T) {
	provider := smtpProviderWithDkim(t, providerID, "", "", "")
	provider.AllowedDomains = []string{"fallback.example.com"}

	u := newHealthUsecase(map[string]*entities.EmailProvider{providerID: provider}, fakeResolver{})

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID})
	if res.GetDomain() != "fallback.example.com" {
		t.Errorf("checked %q, want the allowed domain — a provider that sends but does not sign still wants an answer", res.GetDomain())
	}
}

func TestADomainCanBeCheckedBeforeAProviderExists(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{
		txt: map[string][]string{"example.com": {"v=spf1 include:_spf.example.com ~all"}},
	})

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{
		Domain:    "example.com",
		Selectors: []string{"s1"},
	})

	if !res.GetSpf().GetFound() {
		t.Error("SPF should have been checked for a bare domain")
	}
	// No provider means no private key, so there is nothing to compare against
	// and saying so is more honest than a verdict.
	if got := dkimFor(t, res, "s1"); got.GetKeyMatch() != panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNSPECIFIED {
		t.Errorf("key match = %v, want UNSPECIFIED with no key available", got.GetKeyMatch())
	}
}

func TestNeitherProviderNorDomainIsAnError(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{})
	if _, err := u.Check(context.Background(), testTenant, &panmailv1.CheckDomainHealthRequest{}); err == nil {
		t.Error("checking nothing should be an error, not an empty pass")
	}
}

// GetByID is tenant-scoped; a provider id guessed from another tenant must not
// reveal that tenant's signing domain.
func TestAnotherTenantsProviderIsNotChecked(t *testing.T) {
	provider := smtpProviderWithDkim(t, providerID, "private.example.com", "s1", pkcs1PEM(signingKey()))
	provider.TenantID = "99999999-9999-9999-9999-999999999999"

	u := newHealthUsecase(map[string]*entities.EmailProvider{providerID: provider}, fakeResolver{})

	_, err := u.Check(context.Background(), testTenant, &panmailv1.CheckDomainHealthRequest{ProviderId: providerID})
	if err == nil {
		t.Fatal("a provider belonging to another tenant should not be checkable")
	}
	if strings.Contains(err.Error(), "private.example.com") {
		t.Errorf("the error leaks the other tenant's domain: %v", err)
	}
}

func TestAnInvalidTenantIsRejected(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{})
	if _, err := u.Check(context.Background(), "", &panmailv1.CheckDomainHealthRequest{Domain: "example.com"}); err == nil {
		t.Error("an empty tenant should be rejected")
	}
}

// --- the surrounding records -------------------------------------------------

func TestSpfDmarcAndMxAreReported(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{
		txt: map[string][]string{
			"example.com":        {"v=spf1 include:_spf.example.com ~all"},
			"_dmarc.example.com": {"v=DMARC1; p=reject; rua=mailto:dmarc@example.com"},
		},
		mx: map[string][]*net.MX{
			"example.com": {{Host: "mx1.example.com.", Pref: 10}},
		},
	})

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{Domain: "example.com"})

	if !res.GetSpf().GetFound() || !res.GetSpf().GetValid() {
		t.Errorf("SPF = %+v, want found and valid", res.GetSpf())
	}
	if !res.GetDmarc().GetFound() || !res.GetDmarc().GetValid() {
		t.Errorf("DMARC = %+v, want found and valid", res.GetDmarc())
	}
	if !res.GetMx().GetFound() {
		t.Errorf("MX = %+v, want found", res.GetMx())
	}
}

func TestAbsentRecordsReadAsNotFoundRatherThanAsErrors(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{})

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{Domain: "example.com"})

	if res.GetSpf().GetFound() {
		t.Error("SPF should not read as found")
	}
	// A domain that has published nothing is a setup step never done, not a
	// broken resolver, and the advice differs.
	if res.GetSpf().GetError() != "" {
		t.Errorf("SPF error = %q, want empty; nothing published is not a lookup failure", res.GetSpf().GetError())
	}
	if res.GetDmarc().GetError() != "" {
		t.Errorf("DMARC error = %q, want empty", res.GetDmarc().GetError())
	}
}

// A resolver that is down must not read the same as a domain with no records —
// one means "publish this", the other means "we could not tell".
func TestABrokenResolverIsDistinguishedFromAnAbsentRecord(t *testing.T) {
	u := newHealthUsecase(nil, fakeResolver{
		err: map[string]error{"example.com": errors.New("connection refused")},
	})

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{Domain: "example.com"})

	if res.GetSpf().GetError() == "" {
		t.Error("a resolver failure should be reported as an error, not as an absent record")
	}
	if res.GetSpf().GetFound() {
		t.Error("a failed lookup found nothing")
	}
}

// --- selector handling -------------------------------------------------------

func TestSelectorsAreDedupedAndSorted(t *testing.T) {
	u := newHealthUsecase(
		map[string]*entities.EmailProvider{
			providerID: smtpProviderWithDkim(t, providerID, "example.com", "s1", pkcs1PEM(signingKey())),
		},
		fakeResolver{},
	)

	res := check(t, u, &panmailv1.CheckDomainHealthRequest{
		ProviderId: providerID,
		// s1 duplicates the provider's own selector; the blank is what an empty
		// input field sends.
		Selectors: []string{"s2", "s1", "", "  "},
	})

	var got []string
	for _, d := range res.GetDkim() {
		got = append(got, d.GetSelector())
	}
	// Sorted because the panel renders this list and a map's order would
	// reshuffle it on every refresh.
	if len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Errorf("selectors = %v, want [s1 s2] exactly once each", got)
	}
}
