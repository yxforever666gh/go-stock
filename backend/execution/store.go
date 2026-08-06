package execution

import "context"

// ImmutableOrderEventStore appends already frozen order events to an existing
// strategy run. Event construction, sequencing and sealing remain the caller's
// responsibility.
type ImmutableOrderEventStore[Event any] interface {
	AppendOrderEvents(context.Context, string, []Event) error
}
