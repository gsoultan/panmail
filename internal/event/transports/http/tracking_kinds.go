package http

// Tracking link kinds, shared with the send path that signs them.
const (
	trackingKindOpen  = "open"
	trackingKindClick = "click"
	// Unsubscribe links are signed like the others so the endpoint cannot be
	// used to suppress an arbitrary address for an arbitrary tenant.
	trackingKindUnsubscribe = "unsubscribe"
)
