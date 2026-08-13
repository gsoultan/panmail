package worker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// The schedule a failed delivery follows.
//
// Front-loaded because most failures are a restart or a deploy on the
// receiving side and clear within a minute; the long tail is there so an
// endpoint down for an afternoon is still caught when it returns rather than
// having been given up on at lunchtime.
var retrySchedule = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

// nextAttempt reports when to try again, and whether to try at all.
func nextAttempt(attempt int) (time.Duration, bool) {
	if attempt < 1 || attempt > len(retrySchedule) {
		return 0, false
	}
	return retrySchedule[attempt-1], true
}

// retryable reports whether a status code is worth trying again.
//
// The distinction is what the receiver is telling you. A 5xx means the
// endpoint is broken right now and will probably recover. A 4xx means it has
// looked at the request and rejected it — retrying an unchanged request
// against an unchanged opinion just burns attempts and keeps hitting an
// endpoint that has already said no.
//
// The two exceptions are the codes that mean "not now" rather than "not ever":
// 408 is a timeout and 429 is an explicit ask to slow down, and treating a
// request to slow down as permanent failure is the opposite of what was asked.
func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

const (
	// SignatureHeader carries the HMAC of the delivered body.
	SignatureHeader = "X-Panmail-Signature"
	// TimestampHeader is the second half of what is signed, so a captured
	// notification cannot be replayed later against the same signature.
	TimestampHeader = "X-Panmail-Timestamp"
	// EventHeader lets a receiver route without parsing the body.
	EventHeader = "X-Panmail-Event"
	// DeliveryHeader is stable across retries of the same notification, which
	// is what lets a receiver make its own handling idempotent.
	DeliveryHeader = "X-Panmail-Delivery"
)

// sign produces the value for SignatureHeader.
//
// Over the timestamp and the body together, not the body alone: signing only
// the body means a notification captured once can be replayed forever with a
// signature that still verifies. The receiver checks the timestamp is recent
// and then checks the signature covers it.
//
// The format is prefixed so a future scheme can be introduced without a
// receiver having to guess which one it is looking at.
func sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// applySignature sets the headers a receiver needs to trust the request.
//
// A subscription with no secret is sent unsigned rather than not sent: those
// predate signing, and refusing to deliver to them would turn a security
// improvement into an outage for every existing integration.
func applySignature(req *http.Request, secret, event, deliveryID string, body []byte, now time.Time) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(EventHeader, event)
	req.Header.Set(DeliveryHeader, deliveryID)

	if secret == "" {
		return
	}
	ts := now.Unix()
	req.Header.Set(TimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(SignatureHeader, sign(secret, ts, body))
}
