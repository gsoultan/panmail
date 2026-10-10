package usecases

import (
	"bufio"
	"bytes"
	"mime"
	"net/mail"
	"net/textproto"
	"strings"

	"github.com/gsoultan/gsmail"
)

// DeliveryReport is the machine-readable part of a delivery status
// notification (RFC 3464), as the transport that received the message found
// it. A nil report means the message was not one, whatever its subject and
// wording say.
type DeliveryReport struct {
	// Recipients are the per-recipient sections of the message/delivery-status
	// part that name an address.
	Recipients []ReportedRecipient

	// ReturnedHeaders are the headers of the original message, from the
	// text/rfc822-headers or message/rfc822 part the report hands back. Nil
	// when the report returned none, and then it cannot be tied to anything
	// panmail sent.
	ReturnedHeaders textproto.MIMEHeader
}

// ReportedRecipient is one per-recipient section of a delivery report.
type ReportedRecipient struct {
	// Address is the Final-Recipient, or the Original-Recipient when that is
	// all the section carries, as a lower-cased addr-spec.
	Address string
	// Action is the lower-cased Action field: failed, delayed, delivered,
	// relayed or expanded.
	Action     string
	Status     string
	Diagnostic string
}

// maxReportPart bounds how much of a report part is read. The machine-readable
// sections are a few hundred bytes per recipient; returned headers are a few
// kilobytes.
const maxReportPart = 64 << 10

// ParseDeliveryReport finds the delivery-status part among a received message's
// parts and reads it, or returns nil when there is none.
//
// The structural signal is the message/delivery-status part itself. RFC 3464
// defines that media type only as the second part of a multipart/report with
// report-type=delivery-status, and mail clients do not produce it; the subject
// lines and "user unknown" wording this replaces are produced by anyone. The
// structure is not proof on its own -- anyone can build a multipart/report --
// which is why a report must also be tied to a message panmail sent before it
// is acted on. See inboundUsecase.recordBounce.
func ParseDeliveryReport(parts []gsmail.Attachment) *DeliveryReport {
	var report *DeliveryReport
	var returned textproto.MIMEHeader

	for _, part := range parts {
		mediaType, _, err := mime.ParseMediaType(part.ContentType)
		if err != nil {
			continue
		}
		data := part.Data
		if len(data) > maxReportPart {
			data = data[:maxReportPart]
		}

		switch mediaType {
		case "message/delivery-status", "message/global-delivery-status":
			if report == nil {
				report = &DeliveryReport{Recipients: parseStatusFields(data)}
			}
		case "text/rfc822-headers", "message/rfc822", "message/global", "message/global-headers":
			if returned == nil {
				returned = readHeaderBlock(data)
			}
		}
	}

	if report == nil || len(report.Recipients) == 0 {
		return nil
	}
	report.ReturnedHeaders = returned
	return report
}

// parseStatusFields reads the per-recipient sections of a delivery-status
// body. The first block is the per-message section and is skipped; every block
// after it describes one recipient.
func parseStatusFields(data []byte) []ReportedRecipient {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(data)))

	// The per-message section. Some generators put a blank line before it.
	for {
		h, err := r.ReadMIMEHeader()
		if len(h) > 0 || err != nil {
			if err != nil {
				return nil
			}
			break
		}
	}

	var out []ReportedRecipient
	for {
		h, err := r.ReadMIMEHeader()
		if len(h) > 0 {
			address := reportedAddress(h.Get("Final-Recipient"))
			if address == "" {
				address = reportedAddress(h.Get("Original-Recipient"))
			}
			if address != "" {
				out = append(out, ReportedRecipient{
					Address:    address,
					Action:     strings.ToLower(strings.TrimSpace(h.Get("Action"))),
					Status:     strings.TrimSpace(h.Get("Status")),
					Diagnostic: strings.TrimSpace(h.Get("Diagnostic-Code")),
				})
			}
		}
		if err != nil {
			return out
		}
	}
}

// reportedAddress reads `rfc822; user@example.com` into the bare address.
func reportedAddress(field string) string {
	addrType, value, found := strings.Cut(field, ";")
	if !found {
		value = addrType
	} else if t := strings.ToLower(strings.TrimSpace(addrType)); t != "rfc822" && t != "utf-8" {
		// An X.400 or other non-Internet address is not one panmail sent to.
		return ""
	}
	return bareAddress(value)
}

// bareAddress returns the lower-cased addr-spec of an address in either the
// bare or the display-name form, or "" when it is not an address.
func bareAddress(s string) string {
	s = strings.TrimSpace(s)
	if parsed, err := mail.ParseAddress(s); err == nil {
		return strings.ToLower(parsed.Address)
	}
	s = strings.Trim(s, "<>")
	if strings.ContainsAny(s, " \t<>") || !strings.Contains(s, "@") {
		return ""
	}
	return strings.ToLower(s)
}

// readHeaderBlock reads the header section at the start of a returned message.
// A block cut off by maxReportPart still yields the fields read before the cut.
func readHeaderBlock(data []byte) textproto.MIMEHeader {
	h, _ := textproto.NewReader(bufio.NewReader(bytes.NewReader(data))).ReadMIMEHeader()
	if len(h) == 0 {
		return nil
	}
	return h
}
