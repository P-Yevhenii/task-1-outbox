package domain

import "time"

// OrderCompletedEvent is raised when an order transitions to the completed state.
type OrderCompletedEvent struct {
	OrderID     OrderID
	CustomerID  CustomerID
	TotalAmount int64
	CompletedAt time.Time
}

// EventType returns the unique string identifier for this event.
// Used as the discriminator in the outbox table.
func (e OrderCompletedEvent) EventType() string {
	return "order.completed"
}
