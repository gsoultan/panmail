package usecases

// RetentionTrigger asks the retention worker to apply the policy now.
//
// Saving a retention that only takes effect at the next daily pass reads, from
// the settings page, as a setting that did nothing. Implementations must be
// safe to call from a request handler and must not block on the pass they
// start.
type RetentionTrigger interface {
	Trigger()
}
