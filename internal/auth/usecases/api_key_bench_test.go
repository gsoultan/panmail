package usecases

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/auth/entities"
	"github.com/gsoultan/panmail/internal/auth/repositories"
)

// dbRoundTrip stands in for one indexed lookup against a database on another
// host. The query itself is cheap — api_keys has an index on key_hash — so
// what a request actually pays for is the trip and the pooled connection it
// holds while making it.
const dbRoundTrip = 200 * time.Microsecond

type benchAPIKeyRepo struct {
	lookups atomic.Int64
	latency time.Duration
	key     *entities.ApiKey
}

func (r *benchAPIKeyRepo) GetByHash(_ context.Context, _ string) (*entities.ApiKey, error) {
	r.lookups.Add(1)
	if r.latency > 0 {
		time.Sleep(r.latency)
	}
	return r.key, nil
}

func (r *benchAPIKeyRepo) Create(context.Context, *entities.ApiKey) error { return nil }
func (r *benchAPIKeyRepo) GetByID(context.Context, string, string) (*entities.ApiKey, error) {
	return r.key, nil
}
func (r *benchAPIKeyRepo) ListByTenantID(
	context.Context, string, int, string,
) ([]*entities.ApiKey, string, error) {
	return nil, "", nil
}
func (r *benchAPIKeyRepo) Delete(context.Context, string, string) error             { return nil }
func (r *benchAPIKeyRepo) UpdateStatus(context.Context, string, string, bool) error { return nil }
func (r *benchAPIKeyRepo) UpdateLastUsed(context.Context, string) error             { return nil }

var _ repositories.ApiKeyRepository = (*benchAPIKeyRepo)(nil)

// BenchmarkVerifyApiKey measures what authenticating one request costs.
//
// Every authenticated request pays this, which makes it the most-executed
// database query in the process — admission sustains roughly 3,000 requests a
// second, and each one takes a connection from a pool of 25 to ask whether a
// key it has probably already seen is still valid.
func BenchmarkVerifyApiKey(b *testing.B) {
	repo := &benchAPIKeyRepo{
		latency: dbRoundTrip,
		key: &entities.ApiKey{
			ID: "key-1", TenantID: "tenant-1", IsEnabled: true,
			Scopes: []entities.Scope{entities.ScopeEmailSend},
		},
	}
	usecase := NewApiKeyUsecase(repo)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := usecase.VerifyApiKey(ctx, "pm_live_some_key"); err != nil {
			b.Fatalf("VerifyApiKey: %v", err)
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(repo.lookups.Load())/float64(b.N), "db-lookups/op")
}
