package mime

import (
	"strings"
	"testing"
)

// crlf rewrites the convenient \n in test literals to the \r\n that real
// submissions use, so the fixtures exercise the same line endings as the wire.
func crlf(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func TestParseReadsTheShapesClientsActuallySend(t *testing.T) {
	testCases := []struct {
		name        string
		raw         string
		wantFrom    string
		wantTo      []string
		wantCc      []string
		wantSubject string
		wantText    string
		wantHTML    string
		wantProvide string
	}{
		{
			name: "plain text",
			raw: `From: app@example.com
To: user@example.net
Subject: Hello

Body line one.
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "Hello",
			wantText:    "Body line one.\r\n",
		},
		{
			name: "display names are stripped to addresses",
			raw: `From: Panmail App <app@example.com>
To: "Doe, Jane" <jane@example.net>, bob@example.net
Cc: Carol <carol@example.net>
Subject: Greetings

hi
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"jane@example.net", "bob@example.net"},
			wantCc:      []string{"carol@example.net"},
			wantSubject: "Greetings",
			wantText:    "hi\r\n",
		},
		{
			name: "rfc 2047 encoded subject",
			raw: `From: app@example.com
To: user@example.net
Subject: =?utf-8?B?SGVsbG8sIHfDs3JsZA==?=

body
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "Hello, wórld",
			wantText:    "body\r\n",
		},
		{
			name: "quoted printable body",
			raw: `From: app@example.com
To: user@example.net
Subject: QP
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

caf=C3=A9 receipt
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "QP",
			wantText:    "café receipt\r\n",
		},
		{
			name: "multipart alternative keeps both bodies",
			raw: `From: app@example.com
To: user@example.net
Subject: Both
Content-Type: multipart/alternative; boundary="b1"

--b1
Content-Type: text/plain; charset=utf-8

plain version
--b1
Content-Type: text/html; charset=utf-8

<p>html version</p>
--b1--
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "Both",
			// multipart strips the CRLF before a boundary: it delimits the
			// part rather than belonging to the body.
			wantText: "plain version",
			wantHTML: "<p>html version</p>",
		},
		{
			name: "provider header is lifted out",
			raw: `From: app@example.com
To: user@example.net
Subject: Routed
X-Panmail-Provider-Id: 3f1c2b7a-0000-4000-8000-000000000001

routed body
`,
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "Routed",
			wantText:    "routed body\r\n",
			wantProvide: "3f1c2b7a-0000-4000-8000-000000000001",
		},
		{
			name:        "iso-8859-1 body is converted to utf-8",
			raw:         "From: app@example.com\nTo: user@example.net\nSubject: Latin\nContent-Type: text/plain; charset=iso-8859-1\n\ncaf\xe9\n",
			wantFrom:    "app@example.com",
			wantTo:      []string{"user@example.net"},
			wantSubject: "Latin",
			wantText:    "café\r\n",
		},
	}

	parser := NewParser(1<<20, 10)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := parser.Parse(strings.NewReader(crlf(tc.raw)), "")
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			if msg.Request.From != tc.wantFrom {
				t.Errorf("From = %q, want %q", msg.Request.From, tc.wantFrom)
			}
			if !equalStrings(msg.Request.To, tc.wantTo) {
				t.Errorf("To = %v, want %v", msg.Request.To, tc.wantTo)
			}
			if !equalStrings(msg.Request.Cc, tc.wantCc) {
				t.Errorf("Cc = %v, want %v", msg.Request.Cc, tc.wantCc)
			}
			if msg.Request.Subject != tc.wantSubject {
				t.Errorf("Subject = %q, want %q", msg.Request.Subject, tc.wantSubject)
			}
			if msg.Request.BodyText != tc.wantText {
				t.Errorf("BodyText = %q, want %q", msg.Request.BodyText, tc.wantText)
			}
			if msg.Request.BodyHtml != tc.wantHTML {
				t.Errorf("BodyHtml = %q, want %q", msg.Request.BodyHtml, tc.wantHTML)
			}
			if msg.ProviderID != tc.wantProvide {
				t.Errorf("ProviderID = %q, want %q", msg.ProviderID, tc.wantProvide)
			}
		})
	}
}

func TestParseCarriesAttachments(t *testing.T) {
	raw := crlf(`From: app@example.com
To: user@example.net
Subject: Invoice
Content-Type: multipart/mixed; boundary="m1"

--m1
Content-Type: text/plain; charset=utf-8

See attached.
--m1
Content-Type: application/pdf; name="invoice.pdf"
Content-Disposition: attachment; filename="invoice.pdf"
Content-Transfer-Encoding: base64

aGVsbG8gcGRm
--m1--
`)

	msg, err := NewParser(1<<20, 10).Parse(strings.NewReader(raw), "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got := msg.Request.BodyText; got != "See attached." {
		t.Errorf("BodyText = %q", got)
	}
	if len(msg.Request.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(msg.Request.Attachments))
	}

	attachment := msg.Request.Attachments[0]
	if attachment.Filename != "invoice.pdf" {
		t.Errorf("Filename = %q, want invoice.pdf", attachment.Filename)
	}
	if attachment.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", attachment.ContentType)
	}
	if string(attachment.Content) != "hello pdf" {
		t.Errorf("Content = %q, want %q", attachment.Content, "hello pdf")
	}
}

// A submitted filename is attacker-controlled. Anything downstream that writes
// it to disk must not be handed a path.
func TestParseStripsAPathFromAnAttachmentName(t *testing.T) {
	testCases := []struct {
		name     string
		filename string
		want     string
	}{
		{name: "unix traversal", filename: "../../etc/passwd", want: "passwd"},
		{name: "windows traversal", filename: `..\..\windows\system32\sam`, want: "sam"},
		{name: "absolute path", filename: "/var/lib/secret.key", want: "secret.key"},
		{name: "bare dots", filename: "..", want: "attachment"},
		{name: "ordinary name survives", filename: "report.csv", want: "report.csv"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := crlf(`From: app@example.com
To: user@example.net
Subject: Attachment
Content-Type: multipart/mixed; boundary="m1"

--m1
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="` + tc.filename + `"

payload
--m1--
`)

			msg, err := NewParser(1<<20, 10).Parse(strings.NewReader(raw), "")
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if len(msg.Request.Attachments) != 1 {
				t.Fatalf("got %d attachments, want 1", len(msg.Request.Attachments))
			}
			if got := msg.Request.Attachments[0].Filename; got != tc.want {
				t.Errorf("Filename = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRejectsWhatItCannotSendSafely(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
	}{
		{
			name: "no From address",
			raw: `To: user@example.net
Subject: Anonymous

body
`,
		},
		{
			name: "unparseable From address",
			raw: `From: not an address
To: user@example.net
Subject: Broken

body
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewParser(1<<20, 10).Parse(strings.NewReader(crlf(tc.raw)), ""); err == nil {
				t.Fatal("Parse() succeeded, want an error")
			}
		})
	}
}

// A truncated attachment is worse than a refused one, because nobody finds out.
func TestParseRefusesAnAttachmentOverTheLimit(t *testing.T) {
	raw := crlf(`From: app@example.com
To: user@example.net
Subject: Big
Content-Type: multipart/mixed; boundary="m1"

--m1
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="big.bin"

` + strings.Repeat("A", 512) + `
--m1--
`)

	if _, err := NewParser(64, 10).Parse(strings.NewReader(raw), ""); err == nil {
		t.Fatal("Parse() accepted an oversized attachment, want an error")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
