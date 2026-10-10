package tracking

import (
	"strings"
)

// BounceRefHeader carries, on every copy panmail sends, a reference that a
// delivery report has to hand back before it is believed.
//
// A delivery status notification quotes the headers of the message it reports
// on, so a genuine one for a panmail copy carries this header back. A forged
// one cannot: the value is signed over the tenant, the message and the one
// recipient the copy was addressed to, and nobody but that recipient ever sees
// it.
const BounceRefHeader = "X-Panmail-Ref"

// bounceRefKind keeps a bounce reference from verifying as any other kind of
// signed link, and the reverse.
const bounceRefKind = "bounce"

// bounceRefVersion prefixes the header value, so the format can change without
// an old reference being read under the new rules.
const bounceRefVersion = "v1."

// BounceRef returns the header value for one recipient's copy of a message, or
// "" when the signer has no key -- a reference that would not verify after the
// next restart is worse than none.
//
// The recipient is not written into the value. The report names it, and the
// signature only verifies for that name, so writing it down would disclose the
// address to every system the message passes through for no gain.
func (s *Signer) BounceRef(tenantID, messageID, recipient string) string {
	sig := s.Sign(bounceLink(tenantID, messageID, recipient))
	if sig == "" {
		return ""
	}
	return bounceRefVersion + messageID + "." + sig
}

// VerifyBounceRef checks a reference quoted back in a delivery report against
// the tenant whose mailbox received the report and the recipient the report
// names. It returns the message the reference was minted for.
//
// A reference from another tenant's message, or for another recipient of the
// same message, does not verify.
func (s *Signer) VerifyBounceRef(value, tenantID, recipient string) (messageID string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(value), bounceRefVersion)
	if !ok {
		return "", ErrBadSignature
	}
	// The signature is base64url and has no dot; the message id is the rest.
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return "", ErrMissingSignature
	}
	messageID, sig := rest[:dot], rest[dot+1:]
	if err := s.Verify(bounceLink(tenantID, messageID, recipient), sig); err != nil {
		return "", err
	}
	return messageID, nil
}

func bounceLink(tenantID, messageID, recipient string) Link {
	return Link{
		Kind:      bounceRefKind,
		TenantID:  tenantID,
		MessageID: messageID,
		Recipient: strings.ToLower(strings.TrimSpace(recipient)),
	}
}
