# Code Review: Buggy Interactor

## 1. Why Direct Publish Breaks Reliability

The buggy implementation performs two separate, uncoordinated writes to two different systems:
a database and a message broker. There is no atomic guarantee spanning both. If the process
crashes, the network drops, or the broker is temporarily unavailable between the two calls, the
system ends up in an inconsistent state with no automatic way to recover.

## 2. The Exact Failure Scenario

```
1. uc.db.Update(order)        → SUCCESS — order row is now status=completed in the DB
2. uc.eventBus.Publish(event) → FAILURE — broker timeout / network blip / process crash
```

Result: the order is permanently marked as completed, but `OrderCompletedEvent` is never
delivered. Downstream services (billing, inventory, notifications) never learn that the order was
completed. On a retry attempt the aggregate will return `ErrOrderAlreadyCompleted` and refuse to
transition again, so the event is lost forever.

Additional issues in the same function:

- `order, _ := uc.repo.Retrieve(...)` — error is silently discarded; a DB failure causes a nil
  dereference panic on the next line.
- `order.status = StatusCompleted` — mutates private aggregate state from outside the domain,
  bypassing all business-rule validation and change tracking.
- `order.completedAt = time.Now()` — `time.Now()` is non-deterministic and untestable; the
  event's `CompletedAt` field is also left empty (`Total` is used instead of `TotalAmount`).

## 3. Why the Outbox Must Be in the Same Transaction

The only way to make two writes atomic without a distributed transaction protocol is to put both
in the same ACID database transaction:

```
BEGIN;
  UPDATE orders SET status='completed', completed_at=... WHERE id=...;
  INSERT INTO outbox_events (id, event_type, payload, created_at) VALUES (...);
COMMIT;  -- both succeed, or neither does
```

If the process crashes after `COMMIT`, the outbox row already exists and the background worker
will publish the event on its next poll — **at-least-once delivery** is guaranteed.

If the transaction is rolled back for any reason, neither the order mutation nor the outbox row
is persisted, so the system stays consistent.

Writing the outbox entry in a separate transaction after the order update reintroduces the same
window of failure that the pattern is designed to eliminate.
