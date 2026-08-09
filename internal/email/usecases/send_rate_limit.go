package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/gsoultan/panmail/internal/ratelimit"
	tenantEntities "github.com/gsoultan/panmail/internal/tenant/entities"
	"github.com/gsoultan/panmail/pkg/cache"
)

// SendLimits reports a tenant's send ceiling.
//
// A narrow interface rather than the tenant usecase itself, so the send path
// depends on the one fact it needs instead of on tenant management, and so a
// test can describe a limit without standing up a tenant store.
type SendLimits interface {
	SendLimitFor(ctx context.Context, tenantID string) (ratelimit.Limit, error)
}

// RateLimitedError reports that a tenant is sending faster than it may.
//
// A distinct type because the two callers of the send path have to treat it
// differently and neither can afford to guess. An API client is told to slow
// down and retry; the outbox worker leaves the message queued and comes back,
// because a message that was accepted must not be dropped for arriving during
// a busy minute. Every other error means the send genuinely failed.
type RateLimitedError struct {
	TenantID   string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf(
		"tenant %s is over its send rate; retry in %s",
		e.TenantID, e.RetryAfter.Round(time.Millisecond),
	)
}

// recipientCount is what a send costs against the allowance.
//
// Deliveries, not requests: one call addressed to fifty people is fifty
// messages handed to a provider, and deliveries are what receiving providers
// count when they judge a sending domain. Charging per request would let a
// single call carry unlimited volume, which is exactly the shape of the
// runaway send this exists to stop.
func recipientCount(to, cc, bcc []string) int {
	n := len(to) + len(cc) + len(bcc)
	if n == 0 {
		return 1
	}
	return n
}

// checkSendRate charges this send against the tenant's allowance.
//
// A tenant with no limit configured, or a lookup that fails, is allowed
// through. Failing open is deliberate: the limiter protects a shared sending
// reputation, and a database hiccup that silently stopped all outbound mail
// would be a far worse outage than briefly missing a ceiling.
func (u *sendEmailUsecase) checkSendRate(ctx context.Context, tenantID string, cost int) error {
	if u.limiter == nil || u.sendLimits == nil {
		return nil
	}

	limit, err := u.sendLimits.SendLimitFor(ctx, tenantID)
	if err != nil || limit.Unlimited() {
		return nil
	}

	if allowed, retryAfter := u.limiter.Allow(tenantID, limit, cost); !allowed {
		return &RateLimitedError{TenantID: tenantID, RetryAfter: retryAfter}
	}
	return nil
}

// TenantLookup is the slice of tenant management this needs.
type TenantLookup interface {
	GetTenantByID(ctx context.Context, id string) (*tenantEntities.Tenant, error)
}

// How long a tenant's limit is trusted before being read again. Short enough
// that raising a ceiling during an incident takes effect while the incident is
// still happening, long enough that the send path is not a database read per
// message.
const sendLimitTTL = 30 * time.Second

type tenantSendLimits struct {
	tenants TenantLookup
	cache   *cache.TTLCache[ratelimit.Limit]
}

// NewTenantSendLimits reads send ceilings from the tenant record.
//
// Cached, because this sits on the hot path of every send and the limit
// changes about as often as someone edits a tenant. Reading it per message
// would add a database round trip to the one operation that most needs to stay
// cheap, and the outbox worker would multiply that across a whole queue drain.
func NewTenantSendLimits(tenants TenantLookup) SendLimits {
	return &tenantSendLimits{
		tenants: tenants,
		cache:   cache.New[ratelimit.Limit](sendLimitTTL),
	}
}

func (t *tenantSendLimits) SendLimitFor(ctx context.Context, tenantID string) (ratelimit.Limit, error) {
	if limit, ok := t.cache.Get(tenantID); ok {
		return limit, nil
	}

	tenant, err := t.tenants.GetTenantByID(ctx, tenantID)
	if err != nil {
		return ratelimit.Limit{}, err
	}
	if tenant == nil {
		// No tenant record is not a reason to stop mail that has already been
		// authenticated and accepted; the caller treats this as unlimited.
		return ratelimit.Limit{}, nil
	}

	limit := ratelimit.Limit{
		PerMinute: tenant.SendRatePerMinute,
		Burst:     tenant.SendBurst,
	}
	t.cache.Put(tenantID, limit)
	return limit, nil
}
