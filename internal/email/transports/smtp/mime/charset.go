package mime

import (
	"io"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
)

// passthroughCharsets read back as UTF-8 without conversion, so they skip the
// index entirely. US-ASCII qualifies because it is a subset of UTF-8.
var passthroughCharsets = map[string]struct{}{
	"":         {},
	"utf-8":    {},
	"utf8":     {},
	"us-ascii": {},
	"ascii":    {},
}

// decodeCharset wraps r so its bytes read back as UTF-8.
//
// An unrecognised charset is not an error. Mail from the field carries plenty
// of labels no index knows, and refusing the message would lose it entirely;
// reading the bytes as-is at worst mangles some accented characters in a body
// that would otherwise not have arrived.
func decodeCharset(r io.Reader, label string) io.Reader {
	label = strings.ToLower(strings.TrimSpace(label))
	if _, ok := passthroughCharsets[label]; ok {
		return r
	}

	enc, err := ianaindex.MIME.Encoding(label)
	if err != nil || enc == nil || enc == encoding.Nop {
		return r
	}
	return enc.NewDecoder().Reader(r)
}
