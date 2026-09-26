package usecases

import "fmt"

// Refusals a send can make that are decisions rather than failures.
//
// connect-go renders a bare handler error as CodeUnknown, which a client sees
// as HTTP 500 — "the gateway broke, try again later". For a capacity refusal
// that reading is right, and RateLimitedError and BacklogFullError already
// carry their own code. For the refusals below it is exactly wrong: the
// request will be refused identically forever until somebody changes
// something, so a caller that backs off and retries burns attempts on an
// answer that cannot change.
//
// Each type keeps the message text it replaced, byte for byte. The outbox
// worker classifies a failed send from `e.LastError` — the string, not the
// type — so rewording one of these would quietly change how a retry is
// classified.

// SuppressedRecipientError reports a send addressed to someone on the
// tenant's suppression list.
//
// It refuses the whole message rather than that recipient's copy, and stays
// refused until the suppression is lifted, which is why it is a precondition
// failure rather than a bad argument: the request is well formed and the
// caller is allowed to make it.
type SuppressedRecipientError struct {
	Recipient string
	Reason    string
}

func (e *SuppressedRecipientError) Error() string {
	return fmt.Sprintf("recipient %s is suppressed: %s", e.Recipient, e.Reason)
}

// ProviderNotFoundError reports a send naming a provider this tenant does not
// have. The id came from the caller and no retry will conjure it.
type ProviderNotFoundError struct {
	ProviderID string
}

func (e *ProviderNotFoundError) Error() string {
	return fmt.Sprintf("provider not found: %s", e.ProviderID)
}

// TemplateRefusedError reports a template that cannot produce a message from
// what the caller sent — either the template does not exist, or rendering it
// against this data failed.
//
// Both are the caller's to fix. Note this is deliberately *not* used for a
// failure to read the template from storage: that one is infrastructure, it
// may well succeed on a retry, and it keeps the unknown code on purpose.
type TemplateRefusedError struct {
	TemplateID string
	// Stage is the part that would not render — "subject", "body_html" or
	// "body_text". Empty means the template was not found at all.
	Stage string
	Err   error
}

func (e *TemplateRefusedError) Error() string {
	if e.Stage == "" {
		return fmt.Sprintf("template not found: %s", e.TemplateID)
	}
	return fmt.Sprintf("failed to render %s: %v", e.Stage, e.Err)
}

func (e *TemplateRefusedError) Unwrap() error { return e.Err }

// ProviderDomainRefusedError reports a send whose From domain the provider is
// not authorized for.
//
// A decision, not a failure: the provider's AllowedDomains will answer the
// same way until an operator edits them. It is refused at admission so the
// caller hears it while waiting, and again at delivery, which remains the
// authority because the configuration can change in between.
type ProviderDomainRefusedError struct {
	Provider string
	Domain   string
}

func (e *ProviderDomainRefusedError) Error() string {
	return fmt.Sprintf("provider %s is not authorized to send for domain %s", e.Provider, e.Domain)
}

func (e *ProviderDomainRefusedError) permanent() {}

// permanentRefusal marks a refusal that retrying cannot change.
//
// The outbox worker classifies a failed send from its message, and it read this
// one as retryable: a domain refusal went round the whole default schedule --
// eight retries over roughly two days -- failing identically each time. A
// refusal carrying this marker fails the first time instead. It is opt-in per
// type, so nothing is made permanent by accident.
type permanentRefusal interface {
	permanent()
}
