package usecases

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	evententities "github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// benchEventRepo drops what it is given, so a long benchmark does not measure
// an ever-growing slice, and counts full message reads the way countingRepo
// does.
type benchEventRepo struct {
	backfillingRepo
}

func (*benchEventRepo) Write(context.Context, *evententities.EmailEvent) error { return nil }

// BenchmarkRecordEvent measures the call the send path makes twice per
// recipient. With provider, recipient and subject in hand it must not read
// the stored message — that read decodes the whole body, attachments and all —
// and message-reads/op says whether it did. "lookup" is the case that still
// needs the read, for comparison.
func BenchmarkRecordEvent(b *testing.B) {
	messageID := uuid.NewString()
	cases := []struct {
		name    string
		subject string
	}{
		{"everything-given", "subject"},
		{"lookup", ""},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			repo := &benchEventRepo{}
			repo.messages = map[string]*evententities.EmailMessage{messageID: {
				ID: messageID, TenantID: "t1", ProviderID: "prov-1",
				To: []string{"r@example.com"}, Subject: "stored subject", CreatedAt: time.Now(),
			}}
			uc := NewProcessEventUsecase(repo, nil, nil, &mockProviderRepo{}, nil)
			b.Cleanup(func() { _ = uc.(*processEventUsecase).Close() })
			ctx := context.Background()

			b.ReportAllocs()
			for b.Loop() {
				if err := uc.RecordEvent(ctx, "t1", "prov-1", messageID,
					panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SENT, "r@example.com", tc.subject, "", nil); err != nil {
					b.Fatalf("RecordEvent: %v", err)
				}
			}
			b.ReportMetric(float64(repo.reads)/float64(b.N), "message-reads/op")
		})
	}
}
