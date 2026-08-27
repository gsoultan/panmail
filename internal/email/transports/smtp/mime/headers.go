package mime

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
)

const (
	encodingBase64          = "base64"
	encodingQuotedPrintable = "quoted-printable"
)

// headerValue reads a header from the map form both mail.Header and
// textproto.MIMEHeader reduce to, canonicalising the key first so a part that
// spells it "content-type" is still found.
func headerValue(header map[string][]string, key string) string {
	if values, ok := header[textproto.CanonicalMIMEHeaderKey(key)]; ok && len(values) > 0 {
		return values[0]
	}
	if values, ok := header[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

// singleAddress parses a header expected to hold exactly one address.
func singleAddress(value string, decoder *mime.WordDecoder) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	parser := &mail.AddressParser{WordDecoder: decoder}
	address, err := parser.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse From address %q: %w", value, err)
	}
	return address.Address, nil
}

// addressList parses a header holding any number of addresses. Addresses that
// will not parse are skipped rather than failing the whole submission: one
// malformed entry in a long Cc should not lose the message.
func addressList(value string, decoder *mime.WordDecoder) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parser := &mail.AddressParser{WordDecoder: decoder}
	addresses, err := parser.ParseList(value)
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address.Address != "" {
			out = append(out, address.Address)
		}
	}
	return out
}

// isAttachment reports whether a part should be carried as an attachment
// rather than folded into the body.
func isAttachment(header map[string][]string, mediaType string) bool {
	disposition := headerValue(header, headerDisposition)
	if disposition != "" {
		if kind, _, err := mime.ParseMediaType(disposition); err == nil {
			if strings.EqualFold(kind, dispositionAttachment) {
				return true
			}
		}
	}

	// An inline part still becomes an attachment when it is not text the
	// body can hold — an inline image in a multipart/related, for instance.
	return mediaType != contentTypeTextPlain && mediaType != contentTypeTextHTML
}

// attachmentName recovers a filename from either the disposition or the
// content type, falling back to a generic name so an attachment is never
// dropped for want of one.
func attachmentName(header map[string][]string, contentTypeParams map[string]string) string {
	decoder := &mime.WordDecoder{CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		return decodeCharset(input, charset), nil
	}}

	if disposition := headerValue(header, headerDisposition); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if name := params["filename"]; name != "" {
				return sanitiseName(decodeWord(decoder, name))
			}
		}
	}
	if name := contentTypeParams["name"]; name != "" {
		return sanitiseName(decodeWord(decoder, name))
	}
	return "attachment"
}

// decodeWord decodes an RFC 2047 encoded-word, leaving the value alone when it
// is not encoded or will not decode.
func decodeWord(decoder *mime.WordDecoder, value string) string {
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

// sanitiseName strips path separators from a filename. A submitted name is
// attacker-controlled, and anything downstream that writes it to disk should
// not be handed a traversal.
func sanitiseName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		return "attachment"
	}
	return name
}

// decodeTransferEncoding reverses the Content-Transfer-Encoding of a part.
func decodeTransferEncoding(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case encodingBase64:
		// Mail in the wild wraps base64 at 76 columns, and some agents pad
		// the last line oddly; the lenient decoder keeps those readable.
		return base64.NewDecoder(base64.StdEncoding, newlineStripper{r})
	case encodingQuotedPrintable:
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// newlineStripper drops line breaks so a wrapped base64 body decodes.
type newlineStripper struct {
	inner io.Reader
}

func (s newlineStripper) Read(p []byte) (int, error) {
	// Loop rather than return (0, nil): a read that yielded only line breaks
	// has stripped everything, and handing a caller zero bytes with no error
	// invites it to spin.
	for {
		n, err := s.inner.Read(p)

		kept := 0
		for i := 0; i < n; i++ {
			if p[i] == '\r' || p[i] == '\n' {
				continue
			}
			p[kept] = p[i]
			kept++
		}

		if kept > 0 || err != nil {
			return kept, err
		}
	}
}

// readBounded reads at most limit bytes, failing rather than truncating when
// there is more. A silently truncated attachment is worse than a refused one.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}

	content, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("part is larger than the %d byte limit", limit)
	}
	return content, nil
}
