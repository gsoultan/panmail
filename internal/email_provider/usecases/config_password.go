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
		// SMTP carries four secrets — password, DKIM key, OAuth client secret
		// and OAuth refresh token — and every one has to survive a round trip.
		// An edit that only changed the host would otherwise blank whichever
		// was forgotten: signing would stop silently, or authentication would
		// start failing, with nothing failing loudly at the moment of the edit.
		merged, err := mergePassword(stored, incoming,
			&panmailv1.SmtpConfig{}, &panmailv1.SmtpConfig{},
			func(c *panmailv1.SmtpConfig) string { return c.Password },
			func(c *panmailv1.SmtpConfig, v string) { c.Password = v })
		if err != nil {
			return nil, err
		}
		merged, err = mergePassword(stored, merged,
			&panmailv1.SmtpConfig{}, &panmailv1.SmtpConfig{},
			func(c *panmailv1.SmtpConfig) string { return c.GetDkim().GetPrivateKey() },
			func(c *panmailv1.SmtpConfig, v string) {
				if v == "" {
					return
				}
				if c.Dkim == nil {
					c.Dkim = &panmailv1.DkimConfig{}
				}
				c.Dkim.PrivateKey = v
			})
		if err != nil {
			return nil, err
		}
		merged, err = mergePassword(stored, merged,
			&panmailv1.SmtpConfig{}, &panmailv1.SmtpConfig{},
			func(c *panmailv1.SmtpConfig) string { return c.GetOauth2().GetClientSecret() },
			func(c *panmailv1.SmtpConfig, v string) {
				if v == "" {
					return
				}
				if c.Oauth2 == nil {
					c.Oauth2 = &panmailv1.OAuth2Config{}
				}
				c.Oauth2.ClientSecret = v
			})
		if err != nil {
			return nil, err
		}
		return mergePassword(stored, merged,
			&panmailv1.SmtpConfig{}, &panmailv1.SmtpConfig{},
			func(c *panmailv1.SmtpConfig) string { return c.GetOauth2().GetRefreshToken() },
			func(c *panmailv1.SmtpConfig, v string) {
				if v == "" {
					return
				}
				if c.Oauth2 == nil {
					c.Oauth2 = &panmailv1.OAuth2Config{}
				}
				c.Oauth2.RefreshToken = v
			})
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
	case panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID:
		return mergePassword(stored, incoming,
			&panmailv1.SendGridConfig{}, &panmailv1.SendGridConfig{},
			func(c *panmailv1.SendGridConfig) string { return c.ApiKey },
			func(c *panmailv1.SendGridConfig, v string) { c.ApiKey = v })
	case panmailv1.ProviderType_PROVIDER_TYPE_SES:
		return mergePassword(stored, incoming,
			&panmailv1.SesConfig{}, &panmailv1.SesConfig{},
			func(c *panmailv1.SesConfig) string { return c.SecretKey },
			func(c *panmailv1.SesConfig, v string) { c.SecretKey = v })
	case panmailv1.ProviderType_PROVIDER_TYPE_POSTMARK:
		return mergePassword(stored, incoming,
			&panmailv1.PostmarkConfig{}, &panmailv1.PostmarkConfig{},
			func(c *panmailv1.PostmarkConfig) string { return c.ServerToken },
			func(c *panmailv1.PostmarkConfig, v string) { c.ServerToken = v })
	case panmailv1.ProviderType_PROVIDER_TYPE_MAILGUN:
		return mergePassword(stored, incoming,
			&panmailv1.MailgunConfig{}, &panmailv1.MailgunConfig{},
			func(c *panmailv1.MailgunConfig) string { return c.ApiKey },
			func(c *panmailv1.MailgunConfig, v string) { c.ApiKey = v })
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
