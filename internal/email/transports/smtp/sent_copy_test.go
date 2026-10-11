package smtp

import "testing"

// A mail client expects what it sent in its Sent folder, so every submission
// asks for a copy. Whether one is made is the tenant's setting.
func TestASubmissionAsksForASentCopy(t *testing.T) {
	sender := &stubSender{}
	addr := startTestServer(t, &stubVerifier{}, sender)

	msg := "From: app@example.com\nTo: user@example.net\nSubject: x\n\nhi\n"
	if err := submit(t, addr, testProvider, testAPIKey, "app@example.com", []string{"user@example.net"}, msg); err != nil {
		t.Fatalf("submit: %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.requests) != 1 || !sender.requests[0].SaveSentCopy {
		t.Fatalf("the submission did not ask for a Sent copy")
	}
}
