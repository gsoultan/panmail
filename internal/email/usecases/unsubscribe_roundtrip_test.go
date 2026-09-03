package usecases

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	gsmail "github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	eventhttp "github.com/gsoultan/panmail/internal/event/transports/http"
	eventusecases "github.com/gsoultan/panmail/internal/event/usecases"
	"github.com/gsoultan/panmail/pkg/tracking"
)

// The joint between the two halves of one-click unsubscribe.
//
// Both halves are well covered on their own. The send path has tests that it
// sets the header; the handler has tests that POST unsubscribes, that GET does
// not, that an altered signature is refused. Every one of those builds its own
// URL, so none of them would notice the two sides disagreeing about how a URL
// is shaped — a different segment order, a different encoding of the recipient,
// a different Kind mixed into the signature.
//
// If they did disagree, the header would still be present and RFC 8058 compliant
// and every recipient who clicked unsubscribe would get an error, while panmail
// went on mailing them. That is a legal problem before it is a deliverability
// one, and nothing in either package's tests can see it.
//
// So this takes the URL the send path actually emits and gives it to the real
// handler.
//
// The address matters. The first version of this used person@example.org, whose
// base64 is byte-identical under RawURLEncoding and StdEncoding — 18 bytes needs
// no padding and the output happens to contain no + or /. Swapping the send
// path's encoding for the other one changed nothing and the test still passed,
// which is to say it could not see the failure it was written for. An address
// whose length is not a multiple of three encodes differently under the two, and
// plus-addressing is realistic besides.

type recordingSuppressions struct {
	mu    sync.Mutex
	added []string
}

func (r *recordingSuppressions) Add(_ context.Context, tenantID string, req *panmailv1.AddSuppressionRequest) (*panmailv1.Suppression, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.added = append(r.added, tenantID+"|"+strings.ToLower(req.GetEmail()))
	return &panmailv1.Suppression{Email: req.GetEmail()}, nil
}

func (r *recordingSuppressions) Remove(context.Context, string, string) error { return nil }
func (r *recordingSuppressions) List(context.Context, string, int, string) ([]*panmailv1.Suppression, string, error) {
	return nil, "", nil
}

func (r *recordingSuppressions) Check(_ context.Context, tenantID, email string) (bool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.added {
		if a == tenantID+"|"+strings.ToLower(email) {
			return true, "unsubscribed", nil
		}
	}
	return false, "", nil
}

func (r *recordingSuppressions) has(tenantID, email string) bool {
	ok, _, _ := r.Check(context.Background(), tenantID, email)
	return ok
}

// The handler records an event; nothing here depends on what it does with it.
// Embedding the interface gives the unused methods without listing them, and
// panics loudly if the handler ever starts calling one this test has not
// thought about.
type noopEvents struct {
	eventusecases.ProcessEventUsecase
}

func (noopEvents) RecordEvent(context.Context, string, string, string, panmailv1.EmailEventType, string, string, string, map[string]any) error {
	return nil
}

// listUnsubscribeURL pulls the https target out of the header, the way a mail
// client does: the value is a comma-separated list of <>-wrapped URIs.
var listUnsubscribeURL = regexp.MustCompile(`<(https?://[^>]+)>`)

func TestALinkFromTheSendPathUnsubscribesAtTheHandler(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
	)

	signer := tracking.NewSigner([]byte("a key for the round trip"))

	// The send path's own builder, on a usecase configured the way main does.
	sender := &sendEmailUsecase{
		staticBaseURL:  "https://mail.example.com",
		trackingSigner: signer,
	}

	var msg gsmail.Email
	if err := sender.setUnsubscribeHeaders(&msg, tenantID, messageID, recipient); err != nil {
		t.Fatalf("the send path could not build an unsubscribe link: %v", err)
	}

	header := msg.Headers["List-Unsubscribe"]
	if header == "" {
		t.Fatal("no List-Unsubscribe header was set")
	}
	if post := msg.Headers["List-Unsubscribe-Post"]; !strings.Contains(post, "One-Click") {
		t.Errorf("List-Unsubscribe-Post = %q; RFC 8058 needs List-Unsubscribe=One-Click", post)
	}

	match := listUnsubscribeURL.FindStringSubmatch(header)
	if match == nil {
		t.Fatalf("no URL in the header: %q", header)
	}
	link, err := url.Parse(match[1])
	if err != nil {
		t.Fatalf("the header carries an unparseable URL %q: %v", match[1], err)
	}

	// The real handler, with the same signer the sender used — which is how
	// main wires it, both derived from the one auth key.
	suppressions := &recordingSuppressions{}
	handler := eventhttp.NewUnsubscribeHandler(suppressions, noopEvents{}, signer)

	// What Gmail does with the header: POST, no body of consequence.
	req := httptest.NewRequest(http.MethodPost, link.RequestURI(), strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POSTing the link the send path emitted returned %d: %s\nURL: %s",
			rec.Code, strings.TrimSpace(rec.Body.String()), link.RequestURI())
	}
	if !suppressions.has(tenantID, recipient) {
		t.Error("the recipient was not suppressed; every unsubscribe click would fail " +
			"while panmail kept sending")
	}
}

// The signature has to be the thing that makes it work, not decoration. If the
// handler accepted the link without checking, anyone who guessed the URL shape
// could suppress any address for any tenant.
func TestALinkFromTheSendPathIsRejectedIfAltered(t *testing.T) {
	const (
		tenantID  = "11111111-1111-1111-1111-111111111111"
		messageID = "msg-1"
		recipient = "person+tag@example.org"
	)

	signer := tracking.NewSigner([]byte("a key for the round trip"))
	sender := &sendEmailUsecase{staticBaseURL: "https://mail.example.com", trackingSigner: signer}

	var msg gsmail.Email
	if err := sender.setUnsubscribeHeaders(&msg, tenantID, messageID, recipient); err != nil {
		t.Fatalf("build link: %v", err)
	}
	match := listUnsubscribeURL.FindStringSubmatch(msg.Headers["List-Unsubscribe"])
	if match == nil {
		t.Fatal("no URL in the header")
	}
	link, _ := url.Parse(match[1])

	// Swap the recipient for someone else's address, keeping the signature.
	tampered := strings.Replace(link.RequestURI(),
		encodeRecipientForTest(recipient), encodeRecipientForTest("victim@example.org"), 1)
	if tampered == link.RequestURI() {
		t.Fatal("could not find the recipient segment to alter")
	}

	suppressions := &recordingSuppressions{}
	handler := eventhttp.NewUnsubscribeHandler(suppressions, noopEvents{}, signer)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tampered, nil))

	if rec.Code == http.StatusOK {
		t.Error("an altered link was accepted; anyone could suppress any address")
	}
	if suppressions.has(tenantID, "victim@example.org") {
		t.Error("an address nobody was mailing was suppressed")
	}
}

// encodeRecipientForTest mirrors how the send path puts an address in the path.
func encodeRecipientForTest(addr string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(addr))
}
