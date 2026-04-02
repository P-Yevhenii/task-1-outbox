# Backend Developer Assessment - Senior Level

## Instructions

1. Complete all tasks below
2. Push your solution to a **public GitHub repository**

---

## Task: Domain Events and Outbox Pattern

Design and implement a reliable event-driven order completion system.

### Architecture

```
┌───────────────────────────────────────────────────────────┐
│                  Single Transaction                        │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐            │
│  │  Order   │───>│  Order   │───>│  Outbox  │            │
│  │ Aggregate│    │ Mutation │    │  Entry   │            │
│  └──────────┘    └──────────┘    └──────────┘            │
└───────────────────────────────────────────────────────────┘
                         │
                         ▼
              ┌─────────────────────┐
              │  Background Worker  │
              │  (publishes events) │
              └─────────────────────┘
```

### Requirements

**Domain Events:**
- When an Order is completed, raise `OrderCompletedEvent`
- Events are collected in the aggregate, not published immediately
- Events contain: OrderID, CustomerID, TotalAmount (cents), CompletedAt

**Outbox Pattern:**
- Events stored in outbox table in SAME transaction as aggregate
- Background worker reads and publishes, then marks processed
- Must handle worker crashes (at-least-once delivery)

### Your Implementation

```go
// Domain aggregate with events
type Order struct {
    id          OrderID
    customerID  CustomerID
    totalAmount int64
    status      OrderStatus

    Changes ChangeTracker
    Events  EventRaiser   // You implement
}

func (o *Order) Complete(now time.Time) error {
    // 1. Validate state transition
    // 2. Update fields
    // 3. Track changes
    // 4. Raise OrderCompletedEvent
}

// Repository with outbox
type OrderRepository interface {
    Retrieve(ctx context.Context, id OrderID) (*Order, error)
    UpdateMut(order *Order) *Mutation
    OutboxMuts(order *Order) []*Mutation  // Creates outbox entries for events
}

// Usecase collects ALL mutations
func (uc *Interactor) Execute(ctx context.Context, req *Request) (*Plan, error) {
    // Get aggregate mutation + outbox mutations in single plan
}
```

---

## Buggy Implementation - Find Issues

```go
func (uc *Interactor) Execute(ctx context.Context, req *Request) error {
    order, _ := uc.repo.Retrieve(ctx, req.OrderID)

    order.status = StatusCompleted
    order.completedAt = time.Now()

    // Apply order update
    if err := uc.db.Update(order); err != nil {
        return err
    }

    // Publish event directly
    event := OrderCompletedEvent{
        OrderID:    order.id,
        CustomerID: order.customerID,
        Total:      order.totalAmount,
    }

    if err := uc.eventBus.Publish(event); err != nil {
        return err  // Order already updated!
    }

    return nil
}
```

Create `REVIEW.md` with:
1. Why direct publish breaks reliability
2. The exact failure scenario
3. Why outbox must be same transaction

---

## Questions - Answer in ANSWERS.md

**Q1:** The outbox worker crashes after reading an event but before marking it processed. On restart, it reads the same event again. Why is this "at-least-once" and not "exactly-once"? How should consumers handle this?

**Q2:** Design the outbox table schema. Include:
- All columns with types
- Which indexes and why
- How to prevent processing the same event twice

**Q3:** A customer completes an order, then immediately cancels it. Two events are raised:
- OrderCompletedEvent (t=0)
- OrderCancelledEvent (t=1)

The consumer processes OrderCancelledEvent first (t=1), then OrderCompletedEvent (t=0).

- What goes wrong?
- How do you ensure ordering?

**Q4:** Your outbox has 10 million unprocessed events because a consumer was down for a week. What's your strategy?

**Q5:** This code looks correct but has a subtle bug:

```go
func (r *OrderRepo) OutboxMuts(order *Order) []*Mutation {
    var muts []*Mutation
    for _, event := range order.Events.Events() {
        entry := OutboxEntry{
            ID:        uuid.New().String(),
            EventType: event.EventType(),
            Payload:   toJSON(event),
            CreatedAt: time.Now(),  // BUG HERE
        }
        muts = append(muts, r.outbox.InsertMut(entry))
    }
    return muts
}
```

What's wrong with `time.Now()` here? What should it be instead?

---

## Repository Structure

```
your-repo/
├── domain/
│   ├── order.go
│   ├── events.go
│   └── event_raiser.go
├── contracts/
│   └── repository.go
├── usecases/
│   └── complete_order/
│       ├── interactor.go
│       └── interactor_test.go
├── repo/
│   ├── order_repo.go
│   └── outbox.go
├── REVIEW.md
├── ANSWERS.md
└── SCHEMA.sql          # Outbox table DDL
```

---

## Evaluation

Your submission will be evaluated against our engineering standards document. Key areas:
- Domain events raised in domain methods
- Events collected, not published directly
- Outbox mutations in same plan as aggregate mutations
- EventRaiser properly embedded
- Clear event types with all required data
- Correct outbox schema with idempotency support
