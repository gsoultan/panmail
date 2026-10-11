package mime

import (
	"strings"
)

const (
	headerReplyTo                   = "Reply-To"
	headerInReplyTo                 = "In-Reply-To"
	headerReferences                = "References"
	headerDispositionNotificationTo = "Disposition-Notification-To"
	headerContentID                 = "Content-Id"

	// maxMessageIDLength and maxReferences mirror the limits the send usecase
	// enforces. A submission over them is trimmed here rather than refused:
	// the headers are a convenience to the reader, and losing the message over
	// one of them would be the wrong trade.
	maxMessageIDLength = 255
	maxReferences      = 100
)

// messageIDs extracts the well-formed Message-IDs from a header value.
//
// The value is scanned for <...> tokens and anything else is ignored, which
// covers comments, folding whitespace and the bare IDs some clients write.
// An ID that is not printable ASCII of the form <left@right> is dropped
// rather than failing the submission: a malformed References from a sloppy
// client is a reason to thread less well, not to bounce the message.
func messageIDs(value string) []string {
	var ids []string
	for {
		open := strings.IndexByte(value, '<')
		if open < 0 {
			return ids
		}
		end := strings.IndexByte(value[open:], '>')
		if end < 0 {
			return ids
		}
		id := value[open : open+end+1]
		value = value[open+end+1:]
		if wellFormedMessageID(id) {
			ids = append(ids, id)
		}
	}
}

// firstMessageID returns the first well-formed Message-ID in a header value.
func firstMessageID(value string) string {
	if ids := messageIDs(value); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// references returns a References header's IDs, trimmed to the limit by
// keeping the root and the most recent, which are what a client threads on.
func references(value string) []string {
	ids := messageIDs(value)
	if len(ids) <= maxReferences {
		return ids
	}
	return append([]string{ids[0]}, ids[len(ids)-maxReferences+1:]...)
}

func wellFormedMessageID(id string) bool {
	if len(id) < 5 || len(id) > maxMessageIDLength || id[0] != '<' || id[len(id)-1] != '>' {
		return false
	}
	inner := id[1 : len(id)-1]
	if !printableToken(inner) {
		return false
	}
	at := strings.IndexByte(inner, '@')
	return at > 0 && at < len(inner)-1
}

// contentID returns a part's Content-ID without its angle brackets, or ""
// when it has none or it is not something a cid: reference could name.
func contentID(header map[string][]string) string {
	id := strings.TrimSpace(headerValue(header, headerContentID))
	id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	if id == "" || len(id) > maxMessageIDLength || !printableToken(id) {
		return ""
	}
	return id
}

// printableToken reports whether s is printable ASCII with no spaces and no
// angle brackets, so it can be written as one header token.
func printableToken(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c > '~' || c == '<' || c == '>' {
			return false
		}
	}
	return true
}
