package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/inbound/repositories/entities"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The poller re-reads the newest messages every tick, so the same email
// arrives here repeatedly. Storing it twice was the visible symptom; the
// expensive ones were downstream — a bounce recorded again, and the tenant's
// webhook fired again for a message that arrived once. Anyone consuming that
// endpoint saw the same delivery failure reported every thirty seconds for as
// long as the message sat in the mailbox.

type fakeInboundRepo struct {
	stores.InboundRepository
	stored    map[string]*entities.InboundEmail
	writes    int
	lookupErr error
}

func newFakeRepo() *fakeInboundRepo {
	return &fakeInboundRepo{stored: map[string]*entities.InboundEmail{}}
}

func (f *fakeInboundRepo) Write(_ context.Context, e *entities.InboundEmail) error {
	f.writes++
	f.stored[e.TenantID+"/"+e.ID] = e
	return nil
}

func (f *fakeInboundRepo) GetByID(_ context.Context, tenantID, id string) (*entities.InboundEmail, error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.stored[tenantID+"/"+id], nil
}

type fakeWebhooks struct{ fired int }

func (f *fakeWebhooks) Enqueue(string, panmailv1.WebhookTriggerEvent, any) { f.fired++ }

const dedupTenant = "11111111-1111-1111-1111-111111111111"

func inbound(id, subject string) *panmailv1.InboundEmail {
	return &panmailv1.InboundEmail{
		Id: id, TenantId: dedupTenant,
		From: "sender@example.com", To: []string{"box@example.com"},
		Subject: subject, BodyText: "hello",
	}
}

func TestTheSameMessageIsStoredOnce(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)
	ctx := context.Background()

	for range 5 {
		if err := u.Process(ctx, inbound("msg-1", "Hello")); err != nil {
			t.Fatalf("process: %v", err)
		}
	}

	if repo.writes != 1 {
		t.Errorf("wrote %d copies of one message, want 1", repo.writes)
	}
}

// The one that actually reached the outside world.
func TestTheWebhookFiresOncePerMessage(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)
	ctx := context.Background()

	for range 5 {
		u.Process(ctx, inbound("msg-1", "Hello"))
	}

	if hooks.fired != 1 {
		t.Errorf("fired %d webhooks for one message, want 1", hooks.fired)
	}
}

func TestDistinctMessagesAreAllProcessed(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		u.Process(ctx, inbound(id, "Hello"))
	}

	if repo.writes != 3 || hooks.fired != 3 {
		t.Errorf("wrote %d and fired %d, want 3 of each", repo.writes, hooks.fired)
	}
}

// Refusing to accept mail because the store could not be read would lose it
// outright; processing it twice is recoverable.
func TestAFailedLookupAcceptsTheMessage(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	repo.lookupErr = errors.New("store unavailable")
	u := NewInboundUsecase(repo, nil, hooks)

	if err := u.Process(context.Background(), inbound("msg-1", "Hello")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if repo.writes != 1 {
		t.Error("a message was dropped because the store could not be read")
	}
}

// Two tenants receiving the same broadcast is legitimate, and the identity is
// tenant-scoped, so neither should suppress the other.
func TestAnotherTenantsCopyIsNotSuppressed(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)
	ctx := context.Background()

	u.Process(ctx, inbound("shared", "Hello"))

	other := inbound("shared", "Hello")
	other.TenantId = "22222222-2222-2222-2222-222222222222"
	u.Process(ctx, other)

	if repo.writes != 2 {
		t.Errorf("wrote %d, want both tenants to keep their own copy", repo.writes)
	}
}

func TestAMessageWithNoIdIsStillAccepted(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)

	// Nothing to deduplicate on, so it is processed rather than discarded.
	if err := u.Process(context.Background(), inbound("", "Hello")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if repo.writes != 1 {
		t.Error("a message with no id was dropped")
	}
}

func TestTheStoredTimestampIsPreserved(t *testing.T) {
	repo, hooks := newFakeRepo(), &fakeWebhooks{}
	u := NewInboundUsecase(repo, nil, hooks)

	when := time.Date(2026, 2, 10, 15, 4, 5, 0, time.UTC)
	e := inbound("msg-1", "Hello")
	e.Timestamp = timestamppb.New(when)
	u.Process(context.Background(), e)

	stored := repo.stored[dedupTenant+"/msg-1"]
	if stored == nil || !stored.Timestamp.Equal(when) {
		t.Errorf("stored timestamp %v, want %v", stored, when)
	}
}
