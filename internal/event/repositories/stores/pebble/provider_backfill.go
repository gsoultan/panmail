package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/panmail/internal/event/repositories/entities"
)

// BackfillMessageProvider records the provider on a stored message that does
// not name one yet, and leaves a message that does alone.
//
// The check is the point. Every DELIVERED event asks, once per recipient, and
// the stored value is the whole message with its attachments base64-encoded
// inside it: decoding it in full to read one field cost a full decode per
// recipient per send. The provider is the third field the encoder writes, so
// reading it is a few tokens into the value, and the full decode and rewrite
// happen only for the one call that actually sets it.
func (s *store) BackfillMessageProvider(ctx context.Context, tenantID, messageID, providerID string) error {
	val, closer, err := s.db.Get(messageKey(tenantID, messageID))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closer.Close()

	if current, err := storedProviderID(val); err != nil || current != "" {
		return err
	}

	var m entities.EmailMessage
	if err := json.Unmarshal(val, &m); err != nil {
		return err
	}
	m.ProviderID = providerID
	return s.WriteMessage(ctx, &m)
}

// storedProviderID reads provider_id from an encoded message without decoding
// the rest of it. It stops at the field, so its cost does not depend on how
// large the body and attachments after it are.
func storedProviderID(val []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(val))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", errors.New("stored message is not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		if key, _ := tok.(string); key == "provider_id" {
			var id string
			if err := dec.Decode(&id); err != nil {
				return "", err
			}
			return id, nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return "", err
		}
	}
	return "", nil
}
