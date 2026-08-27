package mime

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

const (
	// ProviderHeader routes a submission to one of the tenant's providers.
	// It is stripped before the message is handed on: which provider carried
	// a message is the gateway's business, not the recipient's.
	ProviderHeader = "X-Panmail-Provider-Id"

	headerFrom            = "From"
	headerTo              = "To"
	headerCc              = "Cc"
	headerSubject         = "Subject"
	headerContentType     = "Content-Type"
	headerContentEncoding = "Content-Transfer-Encoding"
	headerDisposition     = "Content-Disposition"

	contentTypeTextPlain = "text/plain"
	contentTypeTextHTML  = "text/html"
	contentTypeOctet     = "application/octet-stream"

	dispositionAttachment = "attachment"

	// maxNestingDepth bounds how deep a MIME tree may go. Nested multiparts
	// are legitimate, but a message that nests without end is a way to spend
	// the server's stack on one connection.
	maxNestingDepth = 10
)

// ErrNoFrom is returned when a submission carries no usable From address.
// The gateway will not guess a sender: the provider's AllowedDomains check
// is anchored to it.
var ErrNoFrom = errors.New("message has no From address")

// Parser turns submitted messages into send requests.
//
// It is safe for concurrent use: it holds only limits, never per-message state.
type Parser struct {
	maxAttachmentBytes int64
	maxAttachments     int
}

// NewParser returns a Parser bounded by the given limits. A non-positive limit
// disables that particular bound.
func NewParser(maxAttachmentBytes int64, maxAttachments int) *Parser {
	return &Parser{
		maxAttachmentBytes: maxAttachmentBytes,
		maxAttachments:     maxAttachments,
	}
}

// Parse reads an RFC 5322 message and builds the send request it describes.
//
// envelopeFrom is the address from MAIL FROM, used only when the message
// carries no From header. A submission without one is malformed but
// recoverable, and the envelope sender is where bounces were already going.
// It may be empty, in which case a missing From header is an error.
//
// Recipients come from the headers only. The envelope recipients from RCPT TO
// are the session's business, because the difference between the two lists is
// exactly what a Bcc is, and losing it would expose blind addresses to every
// other recipient.
func (p *Parser) Parse(r io.Reader, envelopeFrom string) (*Message, error) {
	parsed, err := mail.ReadMessage(r)
	if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	decoder := &mime.WordDecoder{CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
		return decodeCharset(input, charset), nil
	}}

	from, err := singleAddress(parsed.Header.Get(headerFrom), decoder)
	if err != nil {
		return nil, err
	}
	if from == "" {
		from = strings.TrimSpace(envelopeFrom)
	}
	if from == "" {
		return nil, ErrNoFrom
	}

	subject, err := decoder.DecodeHeader(parsed.Header.Get(headerSubject))
	if err != nil {
		// A subject that will not decode is not worth losing a message over.
		subject = parsed.Header.Get(headerSubject)
	}

	req := &panmailv1.SendEmailRequest{
		From:    from,
		To:      addressList(parsed.Header.Get(headerTo), decoder),
		Cc:      addressList(parsed.Header.Get(headerCc), decoder),
		Subject: subject,
	}

	body := &bodyParts{}
	if err := p.walk(parsed.Body, headerMap(parsed.Header), body, 0); err != nil {
		return nil, err
	}

	req.BodyText = body.text
	req.BodyHtml = body.html
	req.Attachments = body.attachments

	return &Message{
		ProviderID: strings.TrimSpace(parsed.Header.Get(ProviderHeader)),
		Request:    req,
	}, nil
}

// bodyParts accumulates what a walk of the MIME tree finds.
type bodyParts struct {
	text        string
	html        string
	attachments []*panmailv1.Attachment
}

// headerMap adapts a mail.Header to the header view walk works against, so the
// top-level message and a nested part can share one code path.
func headerMap(h mail.Header) map[string][]string {
	return map[string][]string(h)
}

// walk descends the MIME tree, collecting bodies and attachments.
func (p *Parser) walk(body io.Reader, header map[string][]string, out *bodyParts, depth int) error {
	if depth > maxNestingDepth {
		return fmt.Errorf("message nests more than %d levels deep", maxNestingDepth)
	}

	contentType := headerValue(header, headerContentType)
	if contentType == "" {
		contentType = contentTypeTextPlain
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// An unparseable Content-Type is treated as plain text rather than
		// dropped: the bytes are still probably a body.
		mediaType, params = contentTypeTextPlain, map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("multipart message has no boundary")
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("read multipart: %w", err)
			}
			if err := p.walk(part, part.Header, out, depth+1); err != nil {
				_ = part.Close()
				return err
			}
			_ = part.Close()
		}
	}

	return p.leaf(body, header, mediaType, params, out)
}

// leaf handles a single non-multipart part: either a body or an attachment.
func (p *Parser) leaf(
	body io.Reader,
	header map[string][]string,
	mediaType string,
	params map[string]string,
	out *bodyParts,
) error {
	decoded := decodeTransferEncoding(body, headerValue(header, headerContentEncoding))

	if isAttachment(header, mediaType) {
		return p.attach(decoded, header, mediaType, params, out)
	}

	content, err := readBounded(decodeCharset(decoded, params["charset"]), p.maxAttachmentBytes)
	if err != nil {
		return fmt.Errorf("read body part: %w", err)
	}

	switch mediaType {
	case contentTypeTextHTML:
		out.html = appendPart(out.html, string(content))
	default:
		out.text = appendPart(out.text, string(content))
	}
	return nil
}

// attach reads a part into an attachment.
func (p *Parser) attach(
	body io.Reader,
	header map[string][]string,
	mediaType string,
	params map[string]string,
	out *bodyParts,
) error {
	if p.maxAttachments > 0 && len(out.attachments) >= p.maxAttachments {
		return fmt.Errorf("message carries more than %d attachments", p.maxAttachments)
	}

	content, err := readBounded(body, p.maxAttachmentBytes)
	if err != nil {
		return fmt.Errorf("read attachment: %w", err)
	}

	if mediaType == "" {
		mediaType = contentTypeOctet
	}

	out.attachments = append(out.attachments, &panmailv1.Attachment{
		Filename:    attachmentName(header, params),
		ContentType: mediaType,
		Content:     content,
	})
	return nil
}

// appendPart joins sibling parts of the same kind. A multipart/mixed may carry
// two text parts, and keeping only the last would silently truncate the body.
func appendPart(existing, addition string) string {
	if existing == "" {
		return addition
	}
	if addition == "" {
		return existing
	}
	return existing + "\n" + addition
}
