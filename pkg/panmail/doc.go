// Package panmail is the Go client for sending mail through a panmail
// gateway.
//
// It talks to the same ConnectRPC endpoint the web UI uses, authenticating
// with an API key rather than a session. Create the key in Settings → API
// Keys with the email:send scope; the key carries the tenant, so there is
// nothing else to configure.
//
//	client, err := panmail.New("https://mail.example.com", os.Getenv("PANMAIL_API_KEY"))
//	if err != nil {
//		return err
//	}
//
//	result, err := client.Send(ctx, panmail.Message{
//		ProviderID: "0f8b...",
//		From:       "noreply@example.com",
//		To:         []string{"someone@example.org"},
//		Subject:    "Your receipt",
//		HTML:       "<p>Thanks for your order.</p>",
//	})
//
// Send returns once the gateway has written the message to its outbox, not
// once it has been delivered: result.MessageID is what later delivery events
// and webhooks are keyed by.
//
// # Retries
//
// This client does not retry a send whose outcome it does not know. Sending is
// not idempotent and the gateway has no de-duplication key, so a retry after a
// timeout or a dropped connection is a retry of a message that may already be
// on its way to the recipient. The one exception is a refusal — the gateway
// says plainly that it did not accept the message — which is safe to repeat
// and which WithRateLimitRetries turns on.
//
// # Importing
//
// This package is part of the panmail module, so `go get
// github.com/gsoultan/panmail` brings it in. Only the packages actually
// imported are compiled: this one pulls in the generated API and ConnectRPC,
// not the gateway's database or storage engines.
package panmail
