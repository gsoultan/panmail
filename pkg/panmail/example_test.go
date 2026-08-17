package panmail_test

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"github.com/gsoultan/panmail/pkg/panmail"
)

// Compiled, not run: every one of these needs a gateway to talk to. They are
// here so that the documented usage cannot drift from the API, which prose
// examples always eventually do.

func ExampleClient_Send() {
	client, err := panmail.New("https://mail.example.com", os.Getenv("PANMAIL_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.Send(context.Background(), panmail.Message{
		ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
		From:       "noreply@example.com",
		To:         []string{"someone@example.org"},
		Subject:    "Your receipt",
		HTML:       "<p>Thanks for your order.</p>",
		Text:       "Thanks for your order.",
	})
	if err != nil {
		log.Fatal(err)
	}

	// Store this next to whatever prompted the send: delivery events and
	// webhooks are keyed by it.
	log.Printf("queued as %s", result.MessageID)
}

// Sending a stored template rather than a body.
func ExampleClient_Send_template() {
	client, err := panmail.New("https://mail.example.com", os.Getenv("PANMAIL_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Send(context.Background(), panmail.Message{
		ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
		From:       "noreply@example.com",
		To:         []string{"someone@example.org"},
		TemplateID: "order-confirmation",
		TemplateData: map[string]any{
			"name":  "Sam",
			"order": "A-1024",
			"total": 42.5,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Telling the two refusals apart, which is the difference between backing off
// and stopping.
func ExampleClient_Send_refusals() {
	client, err := panmail.New("https://mail.example.com", os.Getenv("PANMAIL_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Send(context.Background(), panmail.Message{
		ProviderID: "0f8b8f4e-0000-4000-8000-000000000000",
		From:       "noreply@example.com",
		To:         []string{"someone@example.org"},
		Subject:    "Your receipt",
		Text:       "Thanks for your order.",
	})

	var limited *panmail.RateLimitedError
	var full *panmail.BacklogFullError
	var auth *panmail.AuthError

	switch {
	case err == nil:
		// Accepted.
	case errors.As(err, &limited):
		// Over the configured rate. The message was not accepted, so sending
		// it again after the delay is safe.
		log.Printf("slow down for %s", limited.RetryAfter)
	case errors.As(err, &full):
		// The tenant already has more queued than its rate can drain. There is
		// no delay to wait out; stop sending until the queue clears.
		log.Print("queue is full, stop sending")
	case errors.As(err, &auth):
		// The key is wrong, revoked, or missing the email:send scope. Retrying
		// will never help.
		log.Fatalf("fix the api key: %v", auth)
	default:
		// Unknown outcome. The message may or may not have been accepted, so
		// this is the one case where retrying can send it twice.
		log.Printf("send failed: %v", err)
	}
}

// Letting the client wait out a rate limit, for a batch job where blocking is
// what you want.
func ExampleWithRateLimitRetries() {
	client, err := panmail.New(
		"https://mail.example.com",
		os.Getenv("PANMAIL_API_KEY"),
		panmail.WithRateLimitRetries(3),
		panmail.WithTimeout(15*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = client
}
