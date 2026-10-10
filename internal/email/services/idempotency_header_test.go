package services

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email/usecases"
)

// keyRecorder accepts every send and remembers the key it arrived with.
type keyRecorder struct {
	refusingUsecase
	key   string
	calls int
}

func (u *keyRecorder) SendEmail(_ context.Context, _ string, req *panmailv1.SendEmailRequest) (*panmailv1.SendEmailResponse, error) {
	u.calls++
	u.key = req.IdempotencyKey
	return &panmailv1.SendEmailResponse{MessageId: "m1"}, nil
}

var _ usecases.SendEmailUsecase = (*keyRecorder)(nil)

func TestTheIdempotencyKeyHeaderIsAnAlternativeToTheField(t *testing.T) {
	tests := []struct {
		name, field, header string
		wantKey             string
		wantCode            connect.Code // zero for accepted
	}{
		{name: "field only", field: "k1", wantKey: "k1"},
		{name: "header only", header: "k1", wantKey: "k1"},
		{name: "both, agreeing", field: "k1", header: "k1", wantKey: "k1"},
		{name: "both, disagreeing", field: "k1", header: "k2", wantCode: connect.CodeInvalidArgument},
		{name: "neither", wantKey: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &keyRecorder{}
			req := connect.NewRequest(&panmailv1.SendEmailRequest{IdempotencyKey: tt.field})
			if tt.header != "" {
				req.Header().Set("Idempotency-Key", tt.header)
			}

			_, err := NewEmailService(recorder).SendEmail(context.Background(), req)
			if tt.wantCode != 0 {
				if connect.CodeOf(err) != tt.wantCode {
					t.Fatalf("err = %v; want %v", err, tt.wantCode)
				}
				if recorder.calls != 0 {
					t.Error("a request with two different keys reached the send")
				}
				return
			}
			if err != nil {
				t.Fatalf("SendEmail: %v", err)
			}
			if recorder.key != tt.wantKey {
				t.Errorf("send saw key %q; want %q", recorder.key, tt.wantKey)
			}
		})
	}
}
