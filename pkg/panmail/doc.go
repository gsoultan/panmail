// Package panmail is the Go client for sending mail through a panmail
// gateway.
//
// Deprecated: the clients now live in their own repository,
// github.com/gsoultan/panmail-sdk, in Go, PHP, Java and Node. Move with:
//
//	go get github.com/gsoultan/panmail-sdk
//	import panmail "github.com/gsoultan/panmail-sdk"
//
// The API is unchanged apart from two things: WithConnectOptions is gone,
// because the new client speaks the Connect protocol's JSON mode directly and
// has no ConnectRPC underneath for options to reach — WithHTTPClient covers
// the transport, proxy and TLS cases it was used for. And Status is a string
// type of the SDK's own rather than an alias for the generated enum; the
// constants compare equal to the same wire values.
//
// This package still works and is not going to be removed without notice, but
// fixes land in panmail-sdk. It cannot move with you either way: this module
// is going private, and a private module cannot be go-got.
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
