package tracking

import (
	"errors"
	"testing"
)

func testSigner() *Signer {
	return NewSigner([]byte("a-test-key-of-reasonable-length!"))
}

func openLink() Link {
	return Link{
		Kind:      "open",
		TenantID:  "tenant-1",
		MessageID: "message-1",
		Recipient: "user@example.com",
	}
}

func clickLink(target string) Link {
	l := openLink()
	l.Kind = "click"
	l.TargetURL = target
	return l
}

func TestSignatureRoundTrip(t *testing.T) {
	s := testSigner()

	for _, link := range []Link{openLink(), clickLink("https://example.com/promo")} {
		t.Run(link.Kind, func(t *testing.T) {
			if err := s.Verify(link, s.Sign(link)); err != nil {
				t.Errorf("expected a freshly signed link to verify: %v", err)
			}
		})
	}
}

// Every field is covered, so an event cannot be attributed to a different
// tenant, message or recipient by editing the path.
func TestSignatureCoversEveryField(t *testing.T) {
	s := testSigner()
	original := clickLink("https://example.com/promo")
	signature := s.Sign(original)

	tampered := []struct {
		name string
		link Link
	}{
		{"tenant", func() Link { l := original; l.TenantID = "tenant-2"; return l }()},
		{"message", func() Link { l := original; l.MessageID = "message-2"; return l }()},
		{"recipient", func() Link { l := original; l.Recipient = "someone@else.com"; return l }()},
		{"kind", func() Link { l := original; l.Kind = "open"; return l }()},
		{"target", func() Link { l := original; l.TargetURL = "https://evil.example.net"; return l }()},
	}

	for _, tc := range tampered {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Verify(tc.link, signature); !errors.Is(err, ErrBadSignature) {
				t.Errorf("expected a tampered %s to be rejected, got %v", tc.name, err)
			}
		})
	}
}

func TestUnsignedLinkIsRejected(t *testing.T) {
	if err := testSigner().Verify(openLink(), ""); !errors.Is(err, ErrMissingSignature) {
		t.Error("expected an unsigned link to be rejected")
	}
}

func TestSignatureFromAnotherKeyIsRejected(t *testing.T) {
	link := openLink()
	other := NewSigner([]byte("a-different-key-entirely-here!!!"))

	if err := testSigner().Verify(link, other.Sign(link)); !errors.Is(err, ErrBadSignature) {
		t.Error("expected a signature from another key to be rejected")
	}
}

// Fields are joined with a separator that cannot occur inside them, so values
// cannot be shifted across the boundary to forge a different link.
func TestFieldsCannotBeShifted(t *testing.T) {
	s := testSigner()

	a := Link{Kind: "open", TenantID: "ab", MessageID: "c", Recipient: "d"}
	b := Link{Kind: "open", TenantID: "a", MessageID: "bc", Recipient: "d"}

	if s.Sign(a) == s.Sign(b) {
		t.Error("two different links produced the same signature")
	}
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"https", "https://example.com/a?b=c", false},
		{"http", "http://example.com", false},
		{"javascript", "javascript:alert(1)", true},
		{"data", "data:text/html;base64,PHNjcmlwdD4=", true},
		{"file", "file:///etc/passwd", true},
		{"scheme relative", "//example.com", true},
		{"no host", "https://", true},
		{"empty", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTarget(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateTarget(%q) = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
		})
	}
}
