package smtp

import (
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/email/usecases"
)

// A resubmission after a lost reply to DATA carries the same Message-ID to the
// same envelope, and must reach the pipeline with the same key; a submission
// without a Message-ID carries none and is queued as it always was.
func TestTheMessageIDBecomesTheIdempotencyKey(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	withID := "From: app@example.com\nTo: user@example.net\nMessage-ID: <abc@app.example.com>\nSubject: x\n\nhi\n"
	envelope := []string{"user@example.net", "blind@example.net"}
	for range 2 {
		if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", envelope, withID); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	reversed := []string{"blind@example.net", "USER@example.net"}
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", reversed, withID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	split := []string{"user@example.net"}
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", split, withID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	without := "From: app@example.com\nTo: user@example.net\nSubject: x\n\nhi\n"
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", envelope, without); err != nil {
		t.Fatalf("submit: %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	keys := make([]string, len(sender.requests))
	for i, req := range sender.requests {
		keys[i] = req.IdempotencyKey
	}

	if keys[0] == "" || keys[0] != keys[1] {
		t.Errorf("a resubmission got key %q, the first %q; want the same, non-empty", keys[1], keys[0])
	}
	if keys[2] != keys[0] {
		t.Error("the same envelope in a different order and case made a different key")
	}
	if keys[3] == keys[0] {
		t.Error("a relay's split transaction to other recipients shared the first one's key; it would be refused as a conflict")
	}
	if keys[4] != "" {
		t.Errorf("a submission without a Message-ID got key %q; want none", keys[4])
	}
	if err := usecases.ValidateIdempotencyKey(keys[0]); err != nil {
		t.Errorf("the derived key is not one the pipeline accepts: %v", err)
	}
}

// The same Message-ID to the same recipients with different content is a
// permanent refusal: resubmitting will be refused identically.
func TestAReusedMessageIDIsRefusedPermanently(t *testing.T) {
	sender := &stubSender{err: &usecases.IdempotencyKeyConflictError{MessageID: "m1"}}
	addr := startTestServer(t, &stubVerifier{}, sender)

	body := "From: app@example.com\nTo: user@example.net\nMessage-ID: <abc@app.example.com>\nSubject: x\n\nhi\n"
	err := submit(t, addr, testProvider, testAPIKey, "app@example.com", []string{"user@example.net"}, body)
	if err == nil {
		t.Fatal("submit succeeded, want a refusal")
	}
	assertReplyCode(t, err, codeNotAuthorized)
	if !strings.Contains(err.Error(), "m1") {
		t.Errorf("refusal %q does not name the message already accepted", err)
	}
}
