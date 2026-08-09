package entities

import "time"

type Tenant struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	RetryPattern []string `json:"retry_pattern"`

	// SendRatePerMinute caps how fast this tenant may hand messages to a
	// provider. Zero means unlimited, which is what every existing tenant gets:
	// introducing a ceiling must not start refusing mail that was flowing.
	//
	// The cap exists because tenants share a provider and a sending IP, so an
	// unbounded send does not only harm the tenant that made it — a spike gets
	// the shared domain rate-limited or blocklisted and everyone's delivery
	// degrades behind it.
	SendRatePerMinute int `json:"send_rate_per_minute"`

	// SendBurst is how much may go at once before the rate binds. Sending is
	// naturally bursty, so a bucket holding only one minute's worth would
	// reject the ordinary case of a campaign queued in one go. Zero falls back
	// to one minute's worth.
	SendBurst int `json:"send_burst"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
