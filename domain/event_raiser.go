package domain

// Event is the marker interface for all domain events.
// Every event must identify itself with a unique type string.
type Event interface {
	EventType() string
}

// EventRaiser is embedded inside an aggregate to collect domain events
// that occurred during a business operation.
type EventRaiser struct {
	events []Event
}

// Raise records a domain event.
// Called from inside aggregate methods (e.g. Order.Complete).
func (er *EventRaiser) Raise(e Event) {
	er.events = append(er.events, e)
}

// Events returns all collected events since the aggregate was loaded.
// Called by the repository to build outbox mutations.
func (er *EventRaiser) Events() []Event {
	return er.events
}
