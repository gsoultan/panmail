package postgres

import (
	"context"
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/db"
	"github.com/gsoultan/panmail/pkg/secrets"
)

// Rotation is the step that makes a key replaceable. Keeping both keys lets
// everything be read, but until every value is rewritten the old key can never
// be dropped — and a key that can never be dropped is not a rotation.

type rotator interface {
	RotateSecrets(ctx context.Context) (int, error)
}

func newKey(t *testing.T) string {
	t.Helper()
	k, err := secrets.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func ring(t *testing.T, primary string, retired ...string) *secrets.Keyring {
	t.Helper()
	k, err := secrets.NewKeyring(primary, retired...)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return k
}

// A store and a connection sharing one database, so a second store can be
// opened over the same rows with a different keyring.
func rotationFixture(t *testing.T) (db.Connection, string) {
	t.Helper()
	return storetest.NewConnection(t), newKey(t)
}

func seedProvider(t *testing.T, repo stores.Repository, id, tenantID string) {
	// The fixture is named for readability; the column is a UUID on PostgreSQL
	// and merely a VARCHAR on SQLite. Mapping here keeps call sites saying
	// "k1" while the database gets something it will accept — the difference
	// that let these fixtures pass for as long as only SQLite was run.
	id = storetest.ID(id)

	t.Helper()
	err := repo.Create(context.Background(), &entities.EmailProvider{
		ID:            id,
		TenantID:      tenantID,
		Name:          "Primary",
		Type:          panmailv1.ProviderType_PROVIDER_TYPE_SMTP,
		Config:        []byte(`{"host":"smtp.example.com","password":"hunter2"}`),
		WebhookSecret: "webhook-signing-secret",
	})
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestRotateSecretsMovesEverythingOntoTheNewKey(t *testing.T) {
	conn, oldKey := rotationFixture(t)
	ctx := context.Background()

	before := NewStore(conn, ring(t, oldKey))
	seedProvider(t, before, "prov-1", storetest.TenantA)
	seedProvider(t, before, "prov-2", storetest.TenantB)

	newKeyHex := newKey(t)
	after := NewStore(conn, ring(t, newKeyHex, oldKey))

	rotated, err := after.(rotator).RotateSecrets(ctx)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated != 2 {
		t.Errorf("rotated %d providers, want 2", rotated)
	}

	// The real test: the old key is now genuinely unnecessary.
	final := NewStore(conn, ring(t, newKeyHex))
	p, err := final.GetByID(ctx, storetest.TenantA, storetest.ID("prov-1"))
	if err != nil {
		t.Fatalf("read after rotation without the old key: %v", err)
	}
	if !strings.Contains(string(p.Config), "hunter2") {
		t.Errorf("config did not survive the rotation: %s", p.Config)
	}
	if p.WebhookSecret != "webhook-signing-secret" {
		t.Errorf("webhook secret = %q, want it preserved", p.WebhookSecret)
	}
}

// An interrupted rotation is resumed by running it again, so the pass has to
// be safe to repeat.
func TestRotateSecretsIsIdempotent(t *testing.T) {
	conn, oldKey := rotationFixture(t)
	ctx := context.Background()

	seedProvider(t, NewStore(conn, ring(t, oldKey)), "prov-1", storetest.TenantA)

	newKeyHex := newKey(t)
	after := NewStore(conn, ring(t, newKeyHex, oldKey)).(rotator)

	if rotated, err := after.RotateSecrets(ctx); err != nil || rotated != 1 {
		t.Fatalf("first pass: rotated %d, err %v", rotated, err)
	}
	// Nothing left to do, and saying so is how an operator knows the rotation
	// finished and the old key can go.
	if rotated, err := after.RotateSecrets(ctx); err != nil || rotated != 0 {
		t.Errorf("second pass: rotated %d, err %v; want 0 and no error", rotated, err)
	}
}

func TestRotateSecretsWithNothingToDoIsHarmless(t *testing.T) {
	conn, key := rotationFixture(t)
	ctx := context.Background()

	repo := NewStore(conn, ring(t, key))
	seedProvider(t, repo, "prov-1", storetest.TenantA)

	// Same key on both sides: the values are already where they belong.
	rotated, err := repo.(rotator).RotateSecrets(ctx)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated != 0 {
		t.Errorf("rotated %d providers when the key had not changed", rotated)
	}
}

func TestRotateSecretsOnAnEmptyDeploymentIsHarmless(t *testing.T) {
	conn, key := rotationFixture(t)
	rotated, err := NewStore(conn, ring(t, key)).(rotator).RotateSecrets(context.Background())
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated != 0 {
		t.Errorf("rotated %d from an empty deployment", rotated)
	}
}

// Rewriting a row whose old key is missing would replace a readable secret
// with an unreadable one, so the pass must stop rather than continue.
func TestRotateSecretsStopsWhenAKeyIsMissing(t *testing.T) {
	conn, oldKey := rotationFixture(t)
	ctx := context.Background()

	seedProvider(t, NewStore(conn, ring(t, oldKey)), "prov-1", storetest.TenantA)

	// The old key was dropped too early — the classic mistake.
	orphaned := NewStore(conn, ring(t, newKey(t))).(rotator)

	rotated, err := orphaned.RotateSecrets(ctx)
	if err == nil {
		t.Fatal("rotating without the key that wrote the data should fail loudly")
	}
	if rotated != 0 {
		t.Errorf("rotated %d rows before failing; nothing should have been written", rotated)
	}
	// The error has to identify the row and say how to recover.
	if !strings.Contains(err.Error(), storetest.ID("prov-1")) {
		t.Errorf("error %q does not say which provider stopped it", err)
	}
	if !strings.Contains(err.Error(), secrets.EnvRetiredKeysName) {
		t.Errorf("error %q does not say how to recover", err)
	}

	// And crucially, the data is untouched: adding the key back recovers it.
	recovered := NewStore(conn, ring(t, newKey(t), oldKey))
	p, err := recovered.GetByID(ctx, storetest.TenantA, storetest.ID("prov-1"))
	if err != nil {
		t.Fatalf("read after the failed rotation: %v", err)
	}
	if !strings.Contains(string(p.Config), "hunter2") {
		t.Error("a failed rotation damaged the stored configuration")
	}
}

// A rotation must cover the whole deployment: a key still holding one tenant's
// rows is a key that cannot be retired.
func TestRotateSecretsCoversEveryTenant(t *testing.T) {
	conn, oldKey := rotationFixture(t)
	ctx := context.Background()

	before := NewStore(conn, ring(t, oldKey))
	seedProvider(t, before, "prov-a", storetest.TenantA)
	seedProvider(t, before, "prov-b", storetest.TenantB)

	newKeyHex := newKey(t)
	if _, err := NewStore(conn, ring(t, newKeyHex, oldKey)).(rotator).RotateSecrets(ctx); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	final := NewStore(conn, ring(t, newKeyHex))
	for _, tc := range []struct{ tenant, id string }{
		{storetest.TenantA, storetest.ID("prov-a")},
		{storetest.TenantB, storetest.ID("prov-b")},
	} {
		if _, err := final.GetByID(ctx, tc.tenant, tc.id); err != nil {
			t.Errorf("%s was left on the old key: %v", tc.id, err)
		}
	}
}
