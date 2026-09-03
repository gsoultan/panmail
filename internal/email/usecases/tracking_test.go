package usecases

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/pkg/tracking"
)

const (
	trackTenantID  = "t1"
	trackMessageID = "m1"
	trackRecipient = "user@example.com"
)

func newTrackingUsecase() (*sendEmailUsecase, *tracking.Signer) {
	signer := tracking.NewSigner([]byte("a-test-key-for-tracking-links!!!"))
	return &sendEmailUsecase{staticBaseURL: "http://localhost", trackingSigner: signer}, signer
}

func TestSendEmailUsecase_InjectTracking(t *testing.T) {
	u, _ := newTrackingUsecase()
	recipientEncoded := base64.RawURLEncoding.EncodeToString([]byte(trackRecipient))

	tests := []struct {
		name        string
		html        string
		wantTarget  string
		wantWrapped bool
	}{
		{
			name:        "simple link",
			html:        `<html><body><a href="https://example.com">Click</a></body></html>`,
			wantTarget:  "https://example.com",
			wantWrapped: true,
		},
		{
			name:        "link with query params",
			html:        `<html><body><a href="https://example.com?a=1&b=2">Click</a></body></html>`,
			wantTarget:  "https://example.com?a=1&b=2",
			wantWrapped: true,
		},
		{
			name:        "link with HTML entities is unescaped first",
			html:        `<html><body><a href="https://example.com?a=1&amp;b=2">Click</a></body></html>`,
			wantTarget:  "https://example.com?a=1&b=2",
			wantWrapped: true,
		},
		{
			name:        "single quotes and spaces",
			html:        `<html><body><a  href = 'https://example.com' >Click</a></body></html>`,
			wantTarget:  "https://example.com",
			wantWrapped: true,
		},
		{
			name:        "javascript and tel are left alone",
			html:        `<html><body><a href="javascript:alert(1)">JS</a><a href="tel:123">Tel</a></body></html>`,
			wantWrapped: false,
		},
		{
			name:        "anchors and mailto are left alone",
			html:        `<html><body><a href="#top">Top</a><a href="mailto:a@b.com">Mail</a></body></html>`,
			wantWrapped: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := u.injectTracking(trackTenantID, trackMessageID, trackRecipient, tc.html)

			pixelPrefix := "http://localhost/track/open/" + trackTenantID + "/" + trackMessageID + "/" + recipientEncoded
			if !strings.Contains(got, pixelPrefix) {
				t.Errorf("expected an open pixel in the output, got:\n%s", got)
			}
			if !strings.Contains(got, tracking.SignatureParam+"=") {
				t.Error("expected every generated URL to carry a signature")
			}

			clickPrefix := "http://localhost/track/click/"
			if tc.wantWrapped {
				if !strings.Contains(got, clickPrefix) {
					t.Errorf("expected the link to be wrapped, got:\n%s", got)
				}
				if !strings.Contains(got, "url="+url.QueryEscape(tc.wantTarget)) {
					t.Errorf("expected target %q to be preserved, got:\n%s", tc.wantTarget, got)
				}
			} else if strings.Contains(got, clickPrefix) {
				t.Errorf("did not expect the link to be wrapped, got:\n%s", got)
			}
		})
	}
}

// Every link the send path produces must verify against the same signer the
// tracking handler uses, otherwise real opens and clicks would be discarded.
func TestInjectedLinksVerify(t *testing.T) {
	u, signer := newTrackingUsecase()

	html := u.injectTracking(trackTenantID, trackMessageID, trackRecipient,
		`<html><body><a href="https://example.com/promo?x=1">Click</a></body></html>`)

	openSig := extractSignature(t, html, "/track/open/")
	if err := signer.Verify(tracking.Link{
		Kind:      trackingKindOpen,
		TenantID:  trackTenantID,
		MessageID: trackMessageID,
		Recipient: trackRecipient,
	}, openSig); err != nil {
		t.Errorf("open pixel signature did not verify: %v", err)
	}

	clickSig := extractSignature(t, html, "/track/click/")
	if err := signer.Verify(tracking.Link{
		Kind:      trackingKindClick,
		TenantID:  trackTenantID,
		MessageID: trackMessageID,
		Recipient: trackRecipient,
		TargetURL: "https://example.com/promo?x=1",
	}, clickSig); err != nil {
		t.Errorf("click link signature did not verify: %v", err)
	}
}

// Without a signer, no tracking is injected at all: emitting unsigned links
// would produce events the handler must reject anyway.
func TestNoTrackingWithoutSigner(t *testing.T) {
	u := &sendEmailUsecase{staticBaseURL: "http://localhost"}

	html := `<html><body><a href="https://example.com">Click</a></body></html>`
	if got := u.injectTracking(trackTenantID, trackMessageID, trackRecipient, html); got != html {
		t.Errorf("expected the body to be untouched, got:\n%s", got)
	}
}

// extractSignature pulls the sig parameter out of the first URL containing the
// given path segment.
func extractSignature(t *testing.T, html, segment string) string {
	t.Helper()

	idx := strings.Index(html, segment)
	if idx == -1 {
		t.Fatalf("no %s URL found in:\n%s", segment, html)
	}

	rest := html[idx:]
	end := strings.IndexAny(rest, `"'`)
	if end == -1 {
		t.Fatalf("unterminated URL in:\n%s", html)
	}

	// The generated href escapes the separator for HTML; undo that before
	// parsing the query string.
	raw := strings.ReplaceAll(rest[:end], "&amp;", "&")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse %q: %v", raw, err)
	}

	sig := parsed.Query().Get(tracking.SignatureParam)
	if sig == "" {
		t.Fatalf("no signature in %q", raw)
	}
	return sig
}
