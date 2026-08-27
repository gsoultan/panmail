package smtp

import (
	"errors"
	"fmt"
	"math"
	"time"

	gosmtp "github.com/emersion/go-smtp"

	"github.com/gsoultan/panmail/internal/email/usecases"
)

// Reply codes this transport returns. They are named because the difference
// between a 4xx and a 5xx decides whether a client retries, and that decision
// is the one thing an SMTP transport can get catastrophically wrong.
const (
	codeRateLimited      = 451
	codeBacklogFull      = 452
	codeTemporaryFailure = 451
	codeAuthFailed       = 535
	codeNotAuthorized    = 550
	codeBadArguments     = 501
)

// enhanced status codes, RFC 3463.
var (
	enhancedPolicyRefusal    = gosmtp.EnhancedCode{4, 7, 1}
	enhancedNoStorage        = gosmtp.EnhancedCode{4, 3, 1}
	enhancedTempFailure      = gosmtp.EnhancedCode{4, 3, 0}
	enhancedBadCredentials   = gosmtp.EnhancedCode{5, 7, 8}
	enhancedNotAuthorized    = gosmtp.EnhancedCode{5, 7, 1}
	enhancedBadArguments     = gosmtp.EnhancedCode{5, 5, 4}
	enhancedMessageTooBig    = gosmtp.EnhancedCode{5, 3, 4}
	enhancedNoValidRecipient = gosmtp.EnhancedCode{5, 1, 3}
)

// permanentError refuses a message outright. Use it only where a retry cannot
// possibly succeed, because the sender is told the message will never be
// delivered.
func permanentError(code int, enhanced gosmtp.EnhancedCode, format string, args ...any) *gosmtp.SMTPError {
	return &gosmtp.SMTPError{
		Code:         code,
		EnhancedCode: enhanced,
		Message:      fmt.Sprintf(format, args...),
	}
}

// temporaryError asks the client to try again later.
func temporaryError(code int, enhanced gosmtp.EnhancedCode, format string, args ...any) *gosmtp.SMTPError {
	return &gosmtp.SMTPError{
		Code:         code,
		EnhancedCode: enhanced,
		Message:      fmt.Sprintf(format, args...),
	}
}

// submissionError translates an outcome from the send usecase into a reply.
//
// The default is deliberately temporary. Every error path in the usecase
// returns before the outbox row is written, so a message that failed was not
// queued and a retry cannot duplicate it — which makes a 4xx the safe answer
// for anything unrecognised. The opposite default would discard a message
// because a database was briefly unreachable.
//
// The two capacity refusals are named because an operator reading a log needs
// to tell "this tenant is sending too fast" from "the queue is not draining";
// they are the same answer to the client but different problems to fix.
func submissionError(err error) *gosmtp.SMTPError {
	if err == nil {
		return nil
	}

	var limited *usecases.RateLimitedError
	if errors.As(err, &limited) {
		seconds := int(math.Ceil(limited.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		// SMTP has no Retry-After, so the delay goes in the text, where it
		// reaches an operator reading a bounce even if no client parses it.
		return temporaryError(
			codeRateLimited, enhancedPolicyRefusal,
			"Too many messages, retry in %ds", seconds,
		)
	}

	var full *usecases.BacklogFullError
	if errors.As(err, &full) {
		return temporaryError(
			codeBacklogFull, enhancedNoStorage,
			"Queue is full (%d pending), retry later", full.Pending,
		)
	}

	return temporaryError(
		codeTemporaryFailure, enhancedTempFailure,
		"Message could not be queued, retry later",
	)
}

// retryAfterSeconds reports the delay a rate limit asks for, for logging.
func retryAfterSeconds(err error) time.Duration {
	var limited *usecases.RateLimitedError
	if errors.As(err, &limited) {
		return limited.RetryAfter
	}
	return 0
}
