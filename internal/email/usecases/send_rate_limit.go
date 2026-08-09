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

	// Depth before rate: a queue that is hours deep should be refused even in
	// a quiet second when a token happens to be available, because accepting
	// makes the wait longer for everything already waiting.
	if err := u.checkBacklog(ctx, tenantID, limit); err != nil {
		return err
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

// BacklogFullError reports that a tenant has more queued than its own send
// rate can work through in a reasonable time.
//
// Separate from RateLimitedError because it means something different and
// wants a different response. Over the rate is momentary — wait a second and
// the same request succeeds. A full backlog is not: the queue is hours deep,
// and adding to it makes the wait longer for everything already in it.
type BacklogFullError struct {
	TenantID string
	Pending  int64
	Ceiling  int64
}

func (e *BacklogFullError) Error() string {
	return fmt.Sprintf(
		"tenant %s has %d messages queued, above the ceiling of %d for its send rate",
		e.TenantID, e.Pending, e.Ceiling,
	)
}

// How much queued work a tenant may hold, expressed as time at its own rate.
//
// An hour is long enough that an ordinary campaign queued in one go is never
// refused, and short enough that a runaway loop is stopped while the queue is
// still something an operator can reason about rather than a million rows.
const backlogMinutes = 60

// The count only matters at the margin, so a little staleness is worth not
// putting a COUNT on the hot path of every send. At 60 a minute this lets
// through about five extra messages against a ceiling of 3600.
const backlogCountTTL = 5 * time.Second

// checkBacklog refuses admission when the queue is already deeper than the
// tenant's rate can drain.
//
// Only for tenants with a rate configured. Without one there is no basis for a
// ceiling, and inventing a default would refuse a legitimate large campaign
// from someone who never asked to be limited.
//
// This is what stops the outbox growing without bound, and what stops a
// backlog accumulated during an outage from leaving faster than it arrived —
// the rate paces admission, but nothing paces a drain, so the only lever is to
// not let the backlog get that deep.
func (u *sendEmailUsecase) checkBacklog(ctx context.Context, tenantID string, limit ratelimit.Limit) error {
	if limit.Unlimited() || u.outboxRepo == nil {
		return nil
	}

	ceiling := int64(limit.PerMinute) * backlogMinutes

	var pending int64
	cached := false
	// The struct is built directly in places, so the cache is not guaranteed;
	// without it the count is simply read every time rather than skipped.
	if u.backlogCounts != nil {
		pending, cached = u.backlogCounts.Get(tenantID)
	}
	if !cached {
		counted, err := u.outboxRepo.CountPending(ctx, tenantID)
		if err != nil {
			// Same reasoning as an unreadable limit: a database hiccup must not
			// silently stop outbound mail.
			return nil
		}
		pending = counted
		if u.backlogCounts != nil {
			u.backlogCounts.Put(tenantID, pending)
		}
	}

	if pending >= ceiling {
		return &BacklogFullError{TenantID: tenantID, Pending: pending, Ceiling: ceiling}
	}
	return nil
}
