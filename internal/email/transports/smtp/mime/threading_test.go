package mime

import (
	"slices"
	"strings"
	"testing"
)

// What Outlook sends for a reply with a pasted image: threading headers, a
// Reply-To, a read-receipt request and the image as a multipart/related part
// that the HTML names by cid. Each has to reach the send request, or the
// recipient gets a reply that does not thread, answers the wrong address and
// shows the image as a loose file.
func TestParseCarriesWhatAMailClientReplySends(t *testing.T) {
	raw := crlf(`From: Jane <jane@example.com>
To: bob@example.net
Subject: RE: Plans
Reply-To: Sales <sales@example.com>
In-Reply-To: <orig@mail.example.net>
References: <root@mail.example.net>
 <orig@mail.example.net>
Disposition-Notification-To: Jane <jane@example.com>
Content-Type: multipart/related; boundary="r1"

--r1
Content-Type: text/html; charset=utf-8

<p>See <img src="cid:image001.png@01DA0000.12345670"></p>
--r1
Content-Type: image/png; name="image001.png"
Content-Disposition: inline; filename="image001.png"
Content-ID: <image001.png@01DA0000.12345670>
Content-Transfer-Encoding: base64

aW1hZ2U=
--r1--
`)

	msg, err := NewParser(1<<20, 10).Parse(strings.NewReader(raw), "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	req := msg.Request

	if req.ReplyTo != "sales@example.com" {
		t.Errorf("ReplyTo = %q, want sales@example.com", req.ReplyTo)
	}
	if req.InReplyTo != "<orig@mail.example.net>" {
		t.Errorf("InReplyTo = %q", req.InReplyTo)
	}
	if want := []string{"<root@mail.example.net>", "<orig@mail.example.net>"}; !slices.Equal(req.References, want) {
		t.Errorf("References = %q, want %q", req.References, want)
	}
	if req.DispositionNotificationTo != "jane@example.com" {
		t.Errorf("DispositionNotificationTo = %q", req.DispositionNotificationTo)
	}
	if len(req.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(req.Attachments))
	}
	if got := req.Attachments[0].ContentId; got != "image001.png@01DA0000.12345670" {
		t.Errorf("ContentId = %q, want it without angle brackets", got)
	}
}

// A malformed threading header costs the thread, never the message.
func TestParseDropsThreadingHeadersItCannotUse(t *testing.T) {
	raw := crlf(`From: app@example.com
To: user@example.net
Subject: Hi
Reply-To: not an address
In-Reply-To: no-brackets@example.net
References: <ok@example.net> <has space@example.net> <noat> junk <also-ok@example.net>
Disposition-Notification-To: also not an address

hi
`)

	msg, err := NewParser(1<<20, 10).Parse(strings.NewReader(raw), "")
	if err != nil {
		t.Fatalf("Parse() error = %v, want the message accepted", err)
	}
	req := msg.Request

	if req.ReplyTo != "" || req.InReplyTo != "" || req.DispositionNotificationTo != "" {
		t.Errorf("kept malformed values: ReplyTo=%q InReplyTo=%q DispositionNotificationTo=%q",
			req.ReplyTo, req.InReplyTo, req.DispositionNotificationTo)
	}
	if want := []string{"<ok@example.net>", "<also-ok@example.net>"}; !slices.Equal(req.References, want) {
		t.Errorf("References = %q, want %q", req.References, want)
	}
}

func TestReferencesKeepsTheRootAndTheMostRecent(t *testing.T) {
	var b strings.Builder
	for i := range maxReferences + 20 {
		b.WriteString("<m")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("@example.net> ")
	}
	all := messageIDs(b.String())
	got := references(b.String())

	if len(got) != maxReferences {
		t.Fatalf("kept %d, want %d", len(got), maxReferences)
	}
	if got[0] != all[0] {
		t.Errorf("first = %q, want the root %q", got[0], all[0])
	}
	if got[len(got)-1] != all[len(all)-1] {
		t.Errorf("last = %q, want the newest %q", got[len(got)-1], all[len(all)-1])
	}
}
