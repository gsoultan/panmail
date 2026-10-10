package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/email/repositories/entities"
	"github.com/gsoultan/panmail/internal/email/repositories/stores"
	"github.com/gsoultan/panmail/internal/storetest"
)

func keyedMessage(name string, created time.Time) (*entities.OutboxEmail, *entities.IdempotencyKey) {
	id := storetest.ID(name)
	msg := &entities.OutboxEmail{
		ID: id, TenantID: storetest.TenantA, Request: []byte(`{}`),
		Status: entities.OutboxStatusPending, NextRetryAt: created, CreatedAt: created, UpdatedAt: created,
	}
	key := &entities.IdempotencyKey{
		TenantID: storetest.TenantA, Key: "order-42", RequestHash: "hash-" + name,
		MessageID: id, CreatedAt: created,
	}
	return msg, key
}

// A live key refuses a second message and queues nothing for it; an expired
// one is replaced, and the message written under it is the one it answers.
func TestCreateWithIdempotencyKeyHonoursOnlyALiveKey(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOutboxTestStore(t)
	now := time.Now()
	window := 24 * time.Hour

	old, oldKey := keyedMessage("old", now.Add(-window-time.Hour))
	if err := repo.CreateWithIdempotencyKey(ctx, old, oldKey, old.CreatedAt.Add(-window)); err != nil {
		t.Fatalf("first use: %v", err)
	}

	if got, err := repo.GetIdempotencyKey(ctx, storetest.TenantA, "order-42", now.Add(-window)); err != nil || got != nil {
		t.Fatalf("expired key read back as %+v, %v; want nothing", got, err)
	}

	fresh, freshKey := keyedMessage("fresh", now)
	if err := repo.CreateWithIdempotencyKey(ctx, fresh, freshKey, now.Add(-window)); err != nil {
		t.Fatalf("reuse after expiry: %v", err)
	}
	got, err := repo.GetIdempotencyKey(ctx, storetest.TenantA, "order-42", now.Add(-window))
	if err != nil || got == nil || got.MessageID != fresh.ID || got.RequestHash != "hash-fresh" {
		t.Fatalf("live key = %+v, %v; want the fresh message", got, err)
	}

	dup, dupKey := keyedMessage("dup", now)
	if err := repo.CreateWithIdempotencyKey(ctx, dup, dupKey, now.Add(-window)); !errors.Is(err, stores.ErrIdempotencyKeyTaken) {
		t.Fatalf("second use of a live key: err = %v; want ErrIdempotencyKeyTaken", err)
	}
	if row, err := repo.GetByID(ctx, dup.ID); err != nil || row != nil {
		t.Fatalf("the refused message was queued anyway: %+v, %v", row, err)
	}

	// Scoped to the tenant: B may use A's key.
	other, otherKey := keyedMessage("other", now)
	other.TenantID, otherKey.TenantID = storetest.TenantB, storetest.TenantB
	if err := repo.CreateWithIdempotencyKey(ctx, other, otherKey, now.Add(-window)); err != nil {
		t.Fatalf("another tenant's use of the same key: %v", err)
	}
}

func TestPruneIdempotencyKeysRemovesOnlyExpiredKeys(t *testing.T) {
	ctx := context.Background()
	repo, sqlDB := newOutboxTestStore(t)
	now := time.Now()

	old, oldKey := keyedMessage("old", now.Add(-48*time.Hour))
	oldKey.Key = "old"
	recent, recentKey := keyedMessage("recent", now)
	recentKey.Key = "recent"
	for _, m := range []struct {
		e *entities.OutboxEmail
		k *entities.IdempotencyKey
	}{{old, oldKey}, {recent, recentKey}} {
		if err := repo.CreateWithIdempotencyKey(ctx, m.e, m.k, now.Add(-72*time.Hour)); err != nil {
			t.Fatalf("seeding %s: %v", m.k.Key, err)
		}
	}

	removed, err := repo.PruneIdempotencyKeys(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneIdempotencyKeys: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d keys; want 1", removed)
	}
	var left []string
	rows, err := sqlDB.QueryContext(ctx, `SELECT idempotency_key FROM send_idempotency_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		left = append(left, k)
	}
	if len(left) != 1 || left[0] != "recent" {
		t.Errorf("keys left = %v; want [recent]", left)
	}
}
