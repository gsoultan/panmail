package usecases

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// countingRepo counts full reads of a stored message, which is what decoding
// its attachments costs.
type countingRepo struct {
	mockEventRepo
	reads  int
	writes int
}

func (c *countingRepo) GetMessage(ctx context.Context, tenantID, messageID string) (*evententities.EmailMessage, error) {
	c.reads++
	return c.mockEventRepo.GetMessage(ctx, tenantID, messageID)
}

func (c *countingRepo) WriteMessage(ctx context.Context, m *evententities.EmailMessage) error {
	c.writes++
	return c.mockEventRepo.WriteMessage(ctx, m)
}

// backfillingRepo can also set the provider alone, as both real stores can.
type backfillingRepo struct {
	countingRepo
	backfills int
}

func (b *backfillingRepo) BackfillMessageProvider(_ context.Context, _, messageID, providerID string) error {
	b.backfills++
	if m := b.messages[messageID]; m != nil && m.ProviderID == "" {
		m.ProviderID = providerID
	}
	return nil
}

// The send path records SENT and DELIVERED for every recipient with the
// provider, recipient and subject already in hand, and each RecordEvent read
// the whole stored message anyway — a 5 MiB attachment sent to 50 recipients
// was decoded 150 times. DELIVERED read it a second time to backfill the
// provider, and wrote all of it back to change one field.
func TestRecordEventReadsTheStoredMessageOnlyWhenItNeedsTo(t *testing.T) {
	const recipients = 50
	messageID := uuid.NewString()
	stored := func() *evententities.EmailMessage {
		return &evententities.EmailMessage{
			ID: messageID, TenantID: "t1", To: []string{"only@example.com"},
			Subject: "stored subject", CreatedAt: time.Now(),
		}
	}
	send := func(t *testing.T, uc ProcessEventUsecase) {
		for i := range recipients {
			recipient := "r" + string(rune('a'+i%26)) + "@example.com"
			for _, typ := range []panmailv1.EmailEventType{
				panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT,
				panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED,
			} {
				if err := uc.RecordEvent(t.Context(), "t1", "prov-1", messageID, typ, recipient, "subject", "", nil); err != nil {
					t.Fatalf("RecordEvent: %v", err)
				}
			}
		}
	}

	t.Run("a store that can backfill", func(t *testing.T) {
		repo := &backfillingRepo{}
		repo.messages = map[string]*evententities.EmailMessage{messageID: stored()}
		send(t, NewProcessEventUsecase(repo, nil, nil, &mockProviderRepo{}, nil))

		if repo.reads != 0 || repo.writes != 0 {
			t.Errorf("%d full reads and %d full writes; want none", repo.reads, repo.writes)
		}
		if repo.backfills != recipients {
			t.Errorf("%d backfills; want one per DELIVERED", repo.backfills)
		}
		if repo.messages[messageID].ProviderID != "prov-1" {
			t.Error("the provider was not recorded on the stored message")
		}
	})

	t.Run("a store that cannot", func(t *testing.T) {
		repo := &countingRepo{}
		repo.messages = map[string]*evententities.EmailMessage{messageID: stored()}
		send(t, NewProcessEventUsecase(repo, nil, nil, &mockProviderRepo{}, nil))

		// One read per DELIVERED to see whether the provider is missing, and
		// one rewrite the first time it is. Never a read per SENT.
		if repo.reads != recipients || repo.writes != 1 {
			t.Errorf("%d reads and %d writes; want %d and 1", repo.reads, repo.writes, recipients)
		}
		if repo.messages[messageID].ProviderID != "prov-1" {
			t.Error("the provider was not recorded on the stored message")
		}
	})
}

// What the lookup is for still happens whenever it is needed: a missing field
// is recovered from the stored message, and an id that is not ours is resolved
// to ours through the recipient.
func TestRecordEventStillRecoversWhatIsMissing(t *testing.T) {
	messageID := uuid.NewString()
	tests := []struct {
		name               string
		messageID          string
		provider, subject  string
		recipient          string
		wantReads          int
		wantMessageID      string
		wantSubject        string
		wantProvider       string
		wantRecipientAfter string
	}{
		{"everything given", messageID, "prov-1", "given", "r@example.com", 0, messageID, "given", "prov-1", "r@example.com"},
		{"no subject", messageID, "prov-1", "", "r@example.com", 1, messageID, "stored subject", "prov-1", "r@example.com"},
		{"no provider", messageID, "", "given", "r@example.com", 1, messageID, "given", "prov-stored", "r@example.com"},
		{"no recipient", messageID, "prov-1", "given", "", 1, messageID, "given", "prov-1", "r@example.com"},
		{"a provider's id", "provider-msg-77", "prov-1", "given", "r@example.com", 1, messageID, "given", "prov-1", "r@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &backfillingRepo{}
			repo.messages = map[string]*evententities.EmailMessage{messageID: {
				ID: messageID, TenantID: "t1", ProviderID: "prov-stored", To: []string{"r@example.com"},
				Subject: "stored subject", CreatedAt: time.Now(),
			}}
			uc := NewProcessEventUsecase(repo, nil, nil, &mockProviderRepo{}, nil)

			if err := uc.RecordEvent(t.Context(), "t1", tt.provider, tt.messageID,
				panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, tt.recipient, tt.subject, "", nil); err != nil {
				t.Fatalf("RecordEvent: %v", err)
			}
			if repo.reads != tt.wantReads {
				t.Errorf("%d reads; want %d", repo.reads, tt.wantReads)
			}
			e := repo.events[len(repo.events)-1]
			if e.MessageID != tt.wantMessageID || e.Subject != tt.wantSubject ||
				e.ProviderID != tt.wantProvider || e.Recipient != tt.wantRecipientAfter {
				t.Errorf("recorded %+v", e)
			}
		})
	}
}
