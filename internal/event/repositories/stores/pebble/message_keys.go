package pebble

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// messageKey is where a stored message body lives.
func messageKey(tenantID, id string) []byte {
	return []byte(fmt.Sprintf("messages:%s:%s", tenantID, id))
}

// recipientMessageKeys returns every index key a message is written under, one
// per distinct recipient across To, Cc and Bcc.
//
// Writing and pruning both call this, and they have to. The index key encodes
// the message's own timestamp, so a prune that rebuilds the key even slightly
// differently from the write deletes nothing and leaves an entry pointing at a
// body that no longer exists — a leak that grows for the life of the
// deployment and that no later pass can find, because the only record of what
// the key should have been was the message just deleted. Building it in one
// place is what makes the two agree by construction instead of by inspection.
func recipientMessageKeys(m *entities.EmailMessage) [][]byte {
	tsDesc := descendingTimestamp(m.CreatedAt)

	recipients := make(map[string]struct{}, len(m.To)+len(m.Cc)+len(m.Bcc))
	for _, group := range [][]string{m.To, m.Cc, m.Bcc} {
		for _, recipient := range group {
			if recipient != "" {
				recipients[recipient] = struct{}{}
			}
		}
	}

	keys := make([][]byte, 0, len(recipients))
	for recipient := range recipients {
		keys = append(keys, []byte(fmt.Sprintf("recipient_messages:%s:%s:%s:%s",
			m.TenantID, recipient, tsDesc, m.ID)))
	}
	return keys
}

// descendingTimestamp renders an instant so that lexicographic order over keys
// is newest-first. The zero padding is load-bearing: without a fixed width a
// shorter number sorts ahead of a longer one and the index silently stops
// being ordered.
func descendingTimestamp(at time.Time) string {
	desc := strconv.FormatInt(math.MaxInt64-at.UnixNano(), 10)
	if len(desc) < timestampWidth {
		desc = strings.Repeat("0", timestampWidth-len(desc)) + desc
	}
	return desc
}

const timestampWidth = 19
