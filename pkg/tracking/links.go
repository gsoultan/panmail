package tracking

import (
	"regexp"
	"strings"

	nethtml "golang.org/x/net/html"
)

// UnescapeHrefValue recovers the URL a mail client will actually request from
// the raw bytes of an href attribute in the message source.
//
// It is not html.UnescapeString, which was what this used to be. That function
// resolves character references the way HTML does *in text*, and an attribute is
// not text. HTML5 keeps a list of named references that may be written without
// the closing semicolon, "&copy" for "©" among them, and then carves out an
// exception: inside an attribute, such a reference is left alone when the next
// character is "=" or alphanumeric. The exception exists for exactly the reason
// it matters here — query strings are full of "&name=value" pairs, and a good
// number of the legacy names are ordinary parameter names.
//
// So "?x=1&copy=2" became "?x=1©=2" and "?id=1&reg=uk" became "?id=1®=uk", and
// the recipient was signed through to a URL the author never wrote. The
// signature still verified, because both halves agreed on the corrupted target,
// so this failed as a wrong destination rather than a rejected link — which is
// why nothing caught it.
func UnescapeHrefValue(raw string) string {
	// No reference without an ampersand, and most hrefs have none.
	if strings.IndexByte(raw, '&') < 0 {
		return raw
	}

	// The value is spliced into a synthetic tag below, so it must not carry the
	// quote that delimits it. hrefRegexp cannot capture one, and a caller that
	// passes one gets its input back rather than a truncated URL.
	if strings.Contains(raw, `"`) {
		return raw
	}

	// Almost every real href that has an ampersand at all has it only as
	// "&amp;", because that is what a template engine writes between query
	// parameters. That reference is unambiguous — named, semicolon-terminated,
	// no longer match possible — so when it is the only one present the answer
	// is a plain substitution, and the tokenizer's 4KB of buffers is not worth
	// paying for on every link of every message of every send.
	if onlyAmpReferences(raw) {
		return strings.ReplaceAll(raw, "&amp;", "&")
	}

	return unescapeViaTokenizer(raw)
}

// onlyAmpReferences reports whether every ampersand in s begins "&amp;", which
// is what makes a plain substitution equivalent to parsing.
func onlyAmpReferences(s string) bool {
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '&')
		if j < 0 {
			return true
		}
		i += j
		if !strings.HasPrefix(s[i:], "&amp;") {
			return false
		}
		i += len("&amp;")
	}
	return true
}

// unescapeViaTokenizer defers to the HTML5 tokenizer in golang.org/x/net/html,
// which is the one a browser follows.
//
// The attribute rule has more corners than it first appears — "&notit;" stays
// literal despite the semicolon, because the longest match ends at "not" and the
// next character is alphanumeric, while "&semi;" really does decode to ";" — so
// this defers rather than reimplementing. TestFastPathMatchesTheTokenizer holds
// the substitution above to the same answers.
func unescapeViaTokenizer(raw string) string {
	z := nethtml.NewTokenizer(strings.NewReader(`<a href="` + raw + `">`))
	if z.Next() != nethtml.StartTagToken {
		return raw
	}
	z.TagName()
	for {
		key, val, more := z.TagAttr()
		if string(key) == "href" {
			return string(val)
		}
		if !more {
			return raw
		}
	}
}

// HrefPattern matches an href attribute. Exported so the send path and anything
// that has to reproduce what the send path signed use one definition.
var HrefPattern = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)

// LinkTargets returns the http(s) destinations an HTML body links to, in the
// exact form the send path signs them.
//
// This has to agree with rewriteLinks byte for byte. It is the same regexp and
// the same unescaping because it is the same function: a second implementation
// that drifted would quietly stop recognising links panmail itself sent.
func LinkTargets(htmlBody string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, m := range HrefPattern.FindAllStringSubmatch(htmlBody, -1) {
		target := UnescapeHrefValue(m[1])
		if ValidateTarget(target) != nil {
			continue
		}
		if _, dup := seen[target]; dup {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	return out
}
