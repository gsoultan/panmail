package services

import (
	"strings"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/proto"
)

func TestRetentionChanges(t *testing.T) {
	tests := []struct {
		name        string
		before      *panmailv1.SystemSettings
		after       *panmailv1.SystemSettings
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:       "nothing moved",
			before:     &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(30)},
			after:      &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(30)},
			wantAbsent: []string{"message_content"},
		},
		{
			// The change that removes every body older than a week, seconds
			// after the save. It has to be the loudest line in the log.
			name:        "switching message content on deletes data",
			before:      &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(0)},
			after:       &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(7)},
			wantContain: []string{"message_content forever->7d DELETES DATA NOW"},
		},
		{
			name:        "shortening an existing policy deletes data",
			before:      &panmailv1.SystemSettings{InboundRetentionDays: proto.Int32(90)},
			after:       &panmailv1.SystemSettings{InboundRetentionDays: proto.Int32(30)},
			wantContain: []string{"inbound_mail 90d->30d DELETES DATA NOW"},
		},
		{
			// Recorded, because an operator reading a timeline wants to know
			// retention was turned off -- but not flagged, because keeping data
			// longer destroys nothing.
			name:        "turning a policy off is recorded without the warning",
			before:      &panmailv1.SystemSettings{ArchiveRetentionDays: proto.Int32(30)},
			after:       &panmailv1.SystemSettings{ArchiveRetentionDays: proto.Int32(0)},
			wantContain: []string{"archives 30d->forever"},
			wantAbsent:  []string{"DELETES DATA NOW"},
		},
		{
			name:        "lengthening is recorded without the warning",
			before:      &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(30)},
			after:       &panmailv1.SystemSettings{MessageRetentionDays: proto.Int32(90)},
			wantContain: []string{"message_content 30d->90d"},
			wantAbsent:  []string{"DELETES DATA NOW"},
		},
		{
			// Events are archived to JSONL on their way out and app logs are
			// operational noise. Flagging those too would make the warning mean
			// nothing on the one line where it matters.
			name:        "classes that are archived or replaceable are not flagged",
			before:      &panmailv1.SystemSettings{LogRetentionDays: proto.Int32(90), AppLogRetentionDays: proto.Int32(90)},
			after:       &panmailv1.SystemSettings{LogRetentionDays: proto.Int32(1), AppLogRetentionDays: proto.Int32(1)},
			wantContain: []string{"events 90d->1d", "app_logs 90d->1d"},
			wantAbsent:  []string{"DELETES DATA NOW"},
		},
		{
			name: "several at once are all reported",
			before: &panmailv1.SystemSettings{
				MessageRetentionDays: proto.Int32(0), InboundRetentionDays: proto.Int32(90),
			},
			after: &panmailv1.SystemSettings{
				MessageRetentionDays: proto.Int32(7), InboundRetentionDays: proto.Int32(30),
			},
			wantContain: []string{"message_content", "inbound_mail"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(retentionChanges(tc.before, tc.after), " | ")

			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("changes = %q; want it to contain %q", got, want)
				}
			}
			for _, unwanted := range tc.wantAbsent {
				if strings.Contains(got, unwanted) {
					t.Errorf("changes = %q; want it to NOT contain %q", got, unwanted)
				}
			}
		})
	}
}

func TestRetentionChangesSurvivesNil(t *testing.T) {
	// A failed read must not take the audit line down with it.
	if got := retentionChanges(nil, &panmailv1.SystemSettings{}); got != nil {
		t.Errorf("changes = %v; want none", got)
	}
	if got := retentionChanges(&panmailv1.SystemSettings{}, nil); got != nil {
		t.Errorf("changes = %v; want none", got)
	}
}
