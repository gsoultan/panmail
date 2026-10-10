package usecases

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/pkg/tracking"
)

// linkBody is a newsletter-shaped body: n tracked links, plus an anchor and a
// mailto: that must be left alone.
func linkBody(n int) string {
	var b strings.Builder
	b.WriteString(`<html><body><a href="#top">top</a><a href="mailto:x@example.com">mail</a>`)
	for i := range n {
		fmt.Fprintf(&b, `<p>Item %d <a href="https://shop.example.com/item/%d?ref=news&amp;utm=1">see it</a></p>`, i, i)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func linkRewriter() *sendEmailUsecase {
	return NewSendEmailUsecase(SendEmailDeps{
		BaseURL:        "https://mail.example.com",
		TrackingSigner: tracking.NewSigner([]byte("test-tracking-key")),
	}).(*sendEmailUsecase)
}

// BenchmarkRewriteLinks measures the per-recipient link rewrite. It runs once
// for every recipient of every message, so its cost is multiplied by the size
// of a send.
func BenchmarkRewriteLinks(b *testing.B) {
	u := linkRewriter()
	recipient := "someone@example.com"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))
	for _, links := range []int{1, 20, 100} {
		body := linkBody(links)
		b.Run(fmt.Sprintf("links=%d", links), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = u.rewriteLinks(body, testTenantID, "msg-1", recipient, encoded)
			}
		})
	}
}

// The rewrite used to run the href pattern twice per link: once to find each
// match, then again on the match alone to pull out its submatch. That second
// pass allocated a fresh submatch slice per link per recipient. One pass with
// the indexes is enough.
//
// The budget is per link, loose enough not to police a few allocations either
// way, and tight enough that putting the second regexp pass back fails it.
func TestRewriteLinksMatchesEachLinkOnce(t *testing.T) {
	u := linkRewriter()
	recipient := "someone@example.com"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))

	const links = 50
	body := linkBody(links)

	out := u.rewriteLinks(body, testTenantID, "msg-1", recipient, encoded)
	if got := strings.Count(out, "/track/click/"); got != links {
		t.Fatalf("rewrote %d links, want %d", got, links)
	}
	if !strings.Contains(out, `href="#top"`) || !strings.Contains(out, `href="mailto:x@example.com"`) {
		t.Fatal("an anchor or a mailto: link was rewritten")
	}

	allocs := testing.AllocsPerRun(20, func() {
		_ = u.rewriteLinks(body, testTenantID, "msg-1", recipient, encoded)
	})
	if perLink := allocs / links; perLink > rewriteAllocsPerLinkBudget {
		t.Errorf("rewriting allocated %.1f times per link, over the budget of %d", perLink, rewriteAllocsPerLinkBudget)
	}
}

// About 15 per link today, almost all of them signing the link. The old
// two-pass version with Sprintf was 20.
const rewriteAllocsPerLinkBudget = 17

// The single pass must produce exactly what the old one did: the click handler
// verifies what the send path signed, and a link rewritten differently is a
// link that stops being recognised.
func TestRewriteLinksOutputIsUnchanged(t *testing.T) {
	u := linkRewriter()
	recipient := "someone@example.com"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(recipient))

	reference := func(htmlContent string) string {
		return hrefRegexp.ReplaceAllStringFunc(htmlContent, func(match string) string {
			submatch := hrefRegexp.FindStringSubmatch(match)
			originalURL := tracking.UnescapeHrefValue(submatch[1])
			if tracking.ValidateTarget(originalURL) != nil {
				return match
			}
			signature := u.trackingSigner.Sign(tracking.Link{
				Kind: trackingKindClick, TenantID: testTenantID, MessageID: "msg-1",
				Recipient: recipient, TargetURL: originalURL,
			})
			return fmt.Sprintf(`href="%s/track/click/%s/%s/%s?url=%s&amp;%s=%s"`,
				u.baseURL(), testTenantID, "msg-1", encoded,
				url.QueryEscape(originalURL), tracking.SignatureParam, signature)
		})
	}

	for _, body := range []string{
		"",
		"no links at all",
		`<a href="#only-an-anchor">x</a>`,
		`<a HREF = 'https://example.com/a?b=1&amp;c=2'>x</a> tail`,
		linkBody(5),
	} {
		if got, want := u.rewriteLinks(body, testTenantID, "msg-1", recipient, encoded), reference(body); got != want {
			t.Errorf("rewriteLinks(%q)\n got %q\nwant %q", body, got, want)
		}
	}
}
