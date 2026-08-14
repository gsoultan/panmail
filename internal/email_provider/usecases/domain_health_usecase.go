package usecases

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"google.golang.org/protobuf/encoding/protojson"
)

// DomainHealthUsecase reports whether a sending domain is set up to be believed.
//
// Testing a provider opens a socket to the server and proves it will accept a
// message. It says nothing about whether anyone will accept the message the
// server then sends, which is decided by DNS the sender does not control:
// SPF authorising the host, a DKIM key published where verifiers look, DMARC
// telling recipients what to do when neither aligns.
//
// The gap this closes is DKIM. Signing is configured by pasting a private key,
// and nothing so far checked that its public half was ever published. Mail
// signed with a key that verifiers cannot find, or can find but does not match,
// is worse off than unsigned mail: an unsigned message merely fails to gain
// DKIM's benefit, whereas a broken signature is an active negative signal and
// fails DMARC alignment outright. The failure is completely silent — the
// message sends, the provider tests healthy, and delivery quietly degrades.
type DomainHealthUsecase interface {
	Check(ctx context.Context, tenantID string, req *panmailv1.CheckDomainHealthRequest) (*panmailv1.CheckDomainHealthResponse, error)
}

type domainHealthUsecase struct {
	repo     stores.Repository
	resolver gsmail.Resolver
	timeout  time.Duration
}

// A resolver that is slow or unreachable must not hold a request open. The
// checks run concurrently, so this bounds the whole set, not each lookup.
const domainHealthTimeout = 10 * time.Second

func NewDomainHealthUsecase(repo stores.Repository) DomainHealthUsecase {
	return &domainHealthUsecase{repo: repo, timeout: domainHealthTimeout}
}

// NewDomainHealthUsecaseWithResolver injects the resolver, so the behaviour can
// be tested against records that are not published anywhere.
func NewDomainHealthUsecaseWithResolver(repo stores.Repository, resolver gsmail.Resolver, timeout time.Duration) DomainHealthUsecase {
	if timeout <= 0 {
		timeout = domainHealthTimeout
	}
	return &domainHealthUsecase{repo: repo, resolver: resolver, timeout: timeout}
}

func (u *domainHealthUsecase) Check(ctx context.Context, tenantID string, req *panmailv1.CheckDomainHealthRequest) (*panmailv1.CheckDomainHealthResponse, error) {
	if err := validateTenantID(tenantID); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	target, err := u.resolveTarget(ctx, tenantID, req)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	health, err := gsmail.HealthChecker{Resolver: u.resolver}.
		CheckDomainHealth(ctx, target.domain, target.selectors)
	if err != nil {
		return nil, err
	}

	res := &panmailv1.CheckDomainHealthResponse{
		Domain: health.Domain,
		Spf:    toDnsCheck(health.SPF),
		Dmarc:  toDnsCheck(health.DMARC),
		Mx:     toDnsCheck(health.MX),
	}

	// Alignment, reported alongside DMARC because that is the check it decides.
	//
	// Everything else here asks whether the records are published and correct.
	// This asks the question they cannot: whether the domain being signed is
	// the domain recipients will see. A provider signing for one domain and
	// permitted to send as another produces mail that passes every check above
	// and fails DMARC at the receiver, which is the worst shape a fault can
	// have — nothing anywhere says anything is wrong.
	if summary := CheckAlignment(target.domain, target.allowedDomains, false).Summary(target.domain); summary != "" {
		if res.Dmarc == nil {
			res.Dmarc = &panmailv1.DnsCheck{}
		}
		if res.Dmarc.Details != "" {
			res.Dmarc.Details += " "
		}
		res.Dmarc.Details += summary
	}

	// Map iteration order is random and this list is rendered, so sort it —
	// otherwise the panel reshuffles on every refresh.
	selectors := make([]string, 0, len(health.DKIM))
	for selector := range health.DKIM {
		selectors = append(selectors, selector)
	}
	sort.Strings(selectors)

	for _, selector := range selectors {
		result := health.DKIM[selector]
		check := &panmailv1.DkimSelectorCheck{
			Selector: selector,
			Dns:      toDnsCheck(result),
		}
		// Only worth comparing against a record that was actually found; a
		// missing selector is already reported by the DNS check, and claiming
		// a mismatch on top of it would be two complaints about one problem.
		if result.Found && target.privateKey != "" {
			check.KeyMatch, check.KeyMatchDetails = compareDkimKey(target.privateKey, result.Record)
		}
		res.Dkim = append(res.Dkim, check)
	}

	return res, nil
}

// what to look up, and the key to compare against if there is one
type healthTarget struct {
	domain     string
	selectors  []string
	privateKey string

	// The From domains this provider will accept, so alignment can be judged
	// against them. A signature proves who signed; DMARC asks whether that is
	// the domain the recipient sees, and the two are configured separately.
	allowedDomains []string
}

// resolveTarget works out what to check, from a saved provider or from the
// request directly.
func (u *domainHealthUsecase) resolveTarget(ctx context.Context, tenantID string, req *panmailv1.CheckDomainHealthRequest) (healthTarget, error) {
	target := healthTarget{
		domain:    strings.TrimSpace(req.GetDomain()),
		selectors: cleanSelectors(req.GetSelectors()),
	}

	if req.GetProviderId() == "" {
		if target.domain == "" {
			return healthTarget{}, fmt.Errorf("either provider_id or domain is required")
		}
		return target, nil
	}

	if _, err := uuid.Parse(req.GetProviderId()); err != nil {
		return healthTarget{}, fmt.Errorf("invalid provider id format: %s. provider id must be a valid UUID", req.GetProviderId())
	}

	// GetByID is tenant-scoped, so a provider belonging to another tenant is
	// not found rather than checked.
	provider, err := u.repo.GetByID(ctx, tenantID, req.GetProviderId())
	if err != nil {
		return healthTarget{}, err
	}
	if provider == nil {
		return healthTarget{}, fmt.Errorf("provider %s not found", req.GetProviderId())
	}

	// Only SMTP signs. The API providers hold their own DKIM keys and publish
	// their own guidance, so there is nothing here to compare against — the
	// domain still gets its SPF, DMARC and MX checked.
	if provider.Type == panmailv1.ProviderType_PROVIDER_TYPE_SMTP {
		// Read the stored config rather than going through Get, which redacts
		// the private key — comparing against the redaction sentinel would
		// report every provider as mismatched.
		config := &panmailv1.SmtpConfig{}
		if err := protojson.Unmarshal(provider.Config, config); err != nil {
			return healthTarget{}, err
		}
		if dkim := config.GetDkim(); dkim != nil {
			if d := strings.TrimSpace(dkim.GetDomain()); d != "" {
				target.domain = d
			}
			if s := strings.TrimSpace(dkim.GetSelector()); s != "" {
				target.selectors = append(target.selectors, s)
			}
			target.privateKey = dkim.GetPrivateKey()
		}
		target.allowedDomains = provider.AllowedDomains
	}

	// Falling back to the first allowed domain means a provider that sends but
	// does not sign still gets a useful answer.
	if target.domain == "" && len(provider.AllowedDomains) > 0 {
		target.domain = strings.TrimSpace(provider.AllowedDomains[0])
	}
	if target.domain == "" {
		return healthTarget{}, fmt.Errorf("provider %s has no DKIM domain or allowed domain to check; supply a domain", req.GetProviderId())
	}

	target.selectors = cleanSelectors(target.selectors)
	return target, nil
}

func cleanSelectors(selectors []string) []string {
	seen := make(map[string]struct{}, len(selectors))
	out := make([]string, 0, len(selectors))
	for _, s := range selectors {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func toDnsCheck(r gsmail.HealthResult) *panmailv1.DnsCheck {
	return &panmailv1.DnsCheck{
		Found:   r.Found,
		Valid:   r.Valid,
		Record:  r.Record,
		Details: r.Details,
		Error:   r.Error,
	}
}

// compareDkimKey checks the published key against the one being signed with.
//
// The comparison itself is gsmail's: DKIMPublicKeyRecord derives the public
// half from the same key forms SignDKIM accepts, so the key checked is exactly
// the key signed with. Panmail used to derive it here, which duplicated the
// crypto and only handled a PEM string.
//
// What stays here is the verdict panmail reports, which is three-valued where
// gsmail's is two. A record that is absent, has no p= tag or has been revoked
// is not evidence of a mismatch, and calling it one sends an operator to
// republish a key when the actual fix is to publish one at all. Those cases
// are told apart structurally, from the record, rather than by reading gsmail's
// wording.
func compareDkimKey(privateKey string, record string) (panmailv1.DkimKeyMatch, string) {
	published, ok := dkimPublicKeyTag(record)
	if !ok {
		return panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNKNOWN, "The published record has no p= tag to compare against."
	}
	if published == "" {
		// An empty p= is a revocation, which the DNS check already reports.
		return panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNKNOWN, "The published key has been revoked (p= is empty)."
	}

	expected, err := gsmail.DKIMPublicKeyRecord(privateKey)
	if err != nil {
		return panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_UNKNOWN, "The configured private key could not be read: " + err.Error()
	}
	configured, _ := dkimPublicKeyTag(expected)

	if subtle.ConstantTimeCompare([]byte(configured), []byte(published)) == 1 {
		return panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MATCHES, ""
	}
	return panmailv1.DkimKeyMatch_DKIM_KEY_MATCH_MISMATCH,
		"The key published for this selector belongs to a different key pair, so every signature sent will fail verification. Publish the public half of the configured key, or configure the key matching what is published."
}

// dkimPublicKeyTag pulls p= out of a DKIM TXT record.
func dkimPublicKeyTag(record string) (string, bool) {
	for _, part := range strings.Split(record, ";") {
		part = strings.TrimSpace(part)
		key, value, found := strings.Cut(part, "=")
		if !found || strings.TrimSpace(key) != "p" {
			continue
		}
		// A long key is published as several quoted strings that resolvers
		// join, so the value can arrive with whitespace inside the base64.
		return stripWhitespace(value), true
	}
	return "", false
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '"':
			return -1
		}
		return r
	}, s)
}
