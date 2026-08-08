package usecases

import (
	"fmt"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// preserveStoredPassword carries the existing password forward when an update
// arrives without one.
//
// The API redacts passwords on read, so a client that fetches a provider,
// edits its host and saves it back has no password to send. Without this the
// save would silently blank the credential.
func preserveStoredPassword(providerType panmailv1.ProviderType, stored, incoming []byte) ([]byte, error) {
	switch providerType {
	case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
		return mergePassword(stored, incoming,
			&panmailv1.SmtpConfig{}, &panmailv1.SmtpConfig{},
			func(c *panmailv1.SmtpConfig) string { return c.Password },
			func(c *panmailv1.SmtpConfig, v string) { c.Password = v })
	case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
		return mergePassword(stored, incoming,
			&panmailv1.ImapConfig{}, &panmailv1.ImapConfig{},
			func(c *panmailv1.ImapConfig) string { return c.Password },
			func(c *panmailv1.ImapConfig, v string) { c.Password = v })
	case panmailv1.ProviderType_PROVIDER_TYPE_POP3:
		return mergePassword(stored, incoming,
			&panmailv1.Pop3Config{}, &panmailv1.Pop3Config{},
			func(c *panmailv1.Pop3Config) string { return c.Password },
			func(c *panmailv1.Pop3Config, v string) { c.Password = v })
	default:
		return incoming, nil
	}
}

func mergePassword[T proto.Message](
	stored, incoming []byte,
	storedMsg, incomingMsg T,
	get func(T) string,
	set func(T, string),
) ([]byte, error) {
	if err := protojson.Unmarshal(incoming, incomingMsg); err != nil {
		return nil, fmt.Errorf("failed to read submitted provider configuration: %w", err)
	}

	if get(incomingMsg) != "" {
		return incoming, nil
	}

	if len(stored) > 0 {
		if err := protojson.Unmarshal(stored, storedMsg); err != nil {
			return nil, fmt.Errorf("failed to read stored provider configuration: %w", err)
		}
		set(incomingMsg, get(storedMsg))
	}

	merged, err := protojson.Marshal(incomingMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to write provider configuration: %w", err)
	}
	return merged, nil
}
