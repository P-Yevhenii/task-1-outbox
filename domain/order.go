package domain

import (
	"errors"
	"time"
)

type OrderID string
type CustomerID string

type OrderStatus string

const (
	StatusPending   OrderStatus = "pending"
	StatusActive    OrderStatus = "active"
	StatusCompleted OrderStatus = "completed"
)

var (
	ErrOrderNotFound           = errors.New("order not found")
	ErrOrderAlreadyCompleted   = errors.New("order is already completed")
	ErrInvalidStatusTransition = errors.New("order cannot be completed from its current status")
)

// ChangeTracker The repository reads Changes to build a precise UPDATE mutation,
// avoiding full-object overwrites.
type ChangeTracker struct {
	fields map[string]any
}

func (ct *ChangeTracker) Track(field string, value any) {
	if ct.fields == nil {
		ct.fields = make(map[string]any)
	}
	ct.fields[field] = value
}

func (ct *ChangeTracker) Changes() map[string]any {
	return ct.fields
}

// Order aggregate for the order bounded context.
type Order struct {
	id          OrderID
	customerID  CustomerID
	totalAmount int64
	status      OrderStatus
	completedAt *time.Time

	Changes ChangeTracker

	// Events collects domain events raised during this operation.
	Events EventRaiser
}

// NewOrder creates a fresh order in pending state.
func NewOrder(id OrderID, customerID CustomerID, totalAmountCents int64) *Order {
	return &Order{
		id:          id,
		customerID:  customerID,
		totalAmount: totalAmountCents,
		status:      StatusPending,
	}
}

// Reconstruct rebuilds an Order from persistence without triggering business logic.
// Used only by the repository layer.
func Reconstruct(
	id OrderID,
	customerID CustomerID,
	totalAmountCents int64,
	status OrderStatus,
	completedAt *time.Time,
) *Order {
	return &Order{
		id:          id,
		customerID:  customerID,
		totalAmount: totalAmountCents,
		status:      status,
		completedAt: completedAt,
	}
}

// Complete transitions the order to the completed state.
func (o *Order) Complete(now time.Time) error {
	if o.status == StatusCompleted {
		return ErrOrderAlreadyCompleted
	}
	if o.status != StatusActive {
		return ErrInvalidStatusTransition
	}

	o.status = StatusCompleted
	o.completedAt = &now

	o.Changes.Track("status", o.status)
	o.Changes.Track("completed_at", now)

	o.Events.Raise(OrderCompletedEvent{
		OrderID:     o.id,
		CustomerID:  o.customerID,
		TotalAmount: o.totalAmount,
		CompletedAt: now,
	})

	return nil
}

func (o *Order) ID() OrderID             { return o.id }
func (o *Order) CustomerID() CustomerID  { return o.customerID }
func (o *Order) TotalAmount() int64      { return o.totalAmount }
func (o *Order) Status() OrderStatus     { return o.status }
func (o *Order) CompletedAt() *time.Time { return o.completedAt }
