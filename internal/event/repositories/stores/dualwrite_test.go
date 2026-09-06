package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
	"github.com/gsoultan/panmail/internal/event/repositories/stores"
)

// primaryStub is the Pebble store, as much of it as the wrapper touches.
type primaryStub struct {
	stores.EventRepository // nil: every other method would panic, which is the point

	written  []*entities.EmailEvent
	messages []*entities.EmailMessage
	err      error
}

func (p *primaryStub) Write(_ context.Context, e *entities.EmailEvent) error {
	if p.err != nil {
		return p.err
	}
	p.written = append(p.written, e)
	return nil
}

func (p *primaryStub) WriteMessage(_ context.Context, m *entities.EmailMessage) error {
	if p.err != nil {
		return p.err
	}
	p.messages = append(p.messages, m)
	return nil
}

type shadowStub struct {
	written  []*entities.EmailEvent
	messages []*entities.EmailMessage
	err      error
}

func (s *shadowStub) Write(_ context.Context, e *entities.EmailEvent) error {
	if s.err != nil {
		return s.err
	}
	s.written = append(s.written, e)
	return nil
}

func (s *shadowStub) WriteMessage(_ context.Context, m *entities.EmailMessage) error {
	if s.err != nil {
		return s.err
	}
	s.messages = append(s.messages, m)
	return nil
}

func event(id string) *entities.EmailEvent {
	return &entities.EmailEvent{ID: id, TenantID: "t1", MessageID: "m1", Timestamp: time.Now()}
}

func TestBothStoresReceiveTheEvent(t *testing.T) {
	primary, shadow := &primaryStub{}, &shadowStub{}
	repo := stores.WithShadow(primary, shadow)

	if err := repo.Write(context.Background(), event("e1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(primary.written) != 1 || len(shadow.written) != 1 {
		t.Fatalf("primary got %d, shadow got %d; want 1 each",
			len(primary.written), len(shadow.written))
	}
}

// The order the wrapper writes in is load bearing. Shadowing a write the
// primary rejected puts a row in the shared table that the store everything
// reads from does not have — which turns the comparison this step exists for
// into a source of false divergence.
func TestARejectedWriteIsNotShadowed(t *testing.T) {
	primary := &primaryStub{err: errors.New("pebble is unhappy")}
	shadow := &shadowStub{}
	repo := stores.WithShadow(primary, shadow)

	if err := repo.Write(context.Background(), event("e1")); err == nil {
		t.Fatal("the primary's error was swallowed")
	}
	if len(shadow.written) != 0 {
		t.Errorf("shadow recorded %d events the primary rejected", len(shadow.written))
	}
}

// Without a shadow the wrapper must be the primary itself, so that turning the
// flag off is genuinely a no-op rather than a thin layer that could still fail.
func TestNoShadowReturnsThePrimaryUnwrapped(t *testing.T) {
	primary := &primaryStub{}
	if got := stores.WithShadow(primary, nil); got != stores.EventRepository(primary) {
		t.Error("a nil shadow still wrapped the primary")
	}
	if got := stores.WithShadow(nil, &shadowStub{}); got != nil {
		t.Error("a nil primary produced a non-nil repository")
	}
}

// The gap that end-to-end testing found: shadowing events but not bodies left
// the shared store with every timeline and no message content, so switching
// reads produced a working dashboard with a blank Content tab.
func TestStoredMessagesAreShadowedToo(t *testing.T) {
	primary, shadow := &primaryStub{}, &shadowStub{}
	repo := stores.WithShadow(primary, shadow)

	m := &entities.EmailMessage{ID: "m1", TenantID: "t1", Subject: "hello"}
	if err := repo.WriteMessage(context.Background(), m); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if len(primary.messages) != 1 || len(shadow.messages) != 1 {
		t.Fatalf("primary got %d, shadow got %d; want 1 each",
			len(primary.messages), len(shadow.messages))
	}
}

// A failing shadow must never fail the caller. Nothing reads it during this
// phase, so a send refused because a store nobody queries was busy would be
// the wrong trade.
func TestAFailingShadowDoesNotFailTheWrite(t *testing.T) {
	primary := &primaryStub{}
	shadow := &shadowStub{err: errors.New("the shared store is unhappy")}
	repo := stores.WithShadow(primary, shadow)

	if err := repo.Write(context.Background(), event("e1")); err != nil {
		t.Errorf("a failing shadow failed the event write: %v", err)
	}
	if err := repo.WriteMessage(context.Background(),
		&entities.EmailMessage{ID: "m1", TenantID: "t1"}); err != nil {
		t.Errorf("a failing shadow failed the message write: %v", err)
	}
	if len(primary.written) != 1 || len(primary.messages) != 1 {
		t.Error("the primary did not receive the writes")
	}
}
