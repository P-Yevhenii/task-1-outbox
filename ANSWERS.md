# Answers

## Q1 — At-Least-Once vs. Exactly-Once

**Why it is at-least-once, not exactly-once:**

The worker reads an unprocessed event and publishes it to the message broker. "Marking processed"
is a separate write to the database that happens *after* the publish call returns. If the worker
crashes in the gap between those two steps the outbox row is never marked, so on the next restart
the same event is read again and published a second time. The event was delivered at least once,
but potentially more than once — hence "at-least-once".

Exactly-once would require the publish and the mark-processed to be atomic across two different
systems (the broker and the DB). This is impossible without a distributed two-phase commit, which
most brokers do not support and which carries a significant performance cost.

**How consumers should handle this:**

Consumers must be **idempotent**. The standard approach is to deduplicate on the event's unique
ID:

1. Each `outbox_events` row has a UUID `id` that travels with the event payload.
2. The consumer maintains a `processed_events(event_id UUID PRIMARY KEY)` table.
3. Before processing, the consumer attempts `INSERT INTO processed_events VALUES ($1)`.
   - If the insert succeeds, the event is new — process it.
   - If it fails with a unique-constraint violation, the event was already handled — skip it.
4. The deduplication insert and the business side-effect should be wrapped in the same
   transaction to avoid a similar crash-between-steps problem on the consumer side.

---

## Q2 — Outbox Table Schema

```sql
CREATE TABLE outbox_events (
    id           UUID         PRIMARY KEY,          -- globally unique; doubles as idempotency key for consumers
    event_type   VARCHAR(100) NOT NULL,             -- discriminator, e.g. "order.completed"
    payload      JSONB        NOT NULL,             -- full event data serialised as JSON
    created_at   TIMESTAMPTZ  NOT NULL,             -- logical event time (from the domain event, NOT time.Now())
    processed_at TIMESTAMPTZ  NULL,                 -- NULL = pending; set by worker after successful publish
    attempts     INTEGER      NOT NULL DEFAULT 0    -- incremented on each delivery attempt; used for dead-lettering
);
```

**Indexes:**

```sql
-- Used by the background worker to poll for work efficiently.
-- Partial index keeps it small: completed rows are excluded automatically.
CREATE INDEX idx_outbox_pending ON outbox_events (created_at ASC)
    WHERE processed_at IS NULL;
```

A full index on all rows is wasteful once the table grows; the partial index only covers the
rows the worker actually needs to read.

**Preventing double-processing (producer side):**

The `id` column is a `PRIMARY KEY`. Any attempt to insert a duplicate event (e.g. due to a
retried application request) will fail with a unique-constraint violation, making inserts
naturally idempotent.

**Preventing double-processing (consumer side):**

The consumer uses the `id` as a deduplication key in its own `processed_events` table, as
described in Q1.

---

## Q3 — Out-of-Order Event Processing

**What goes wrong:**

The consumer receives `OrderCancelledEvent` (t=1) first and cancels the order. It then receives
`OrderCompletedEvent` (t=0) and completes the order. The final persisted state is `completed`
even though the customer's last action was a cancellation. Downstream services (refunds,
inventory) may also execute in the wrong sequence.

**How to ensure ordering:**

1. **Partition by aggregate ID.** Use the `OrderID` as the message key when publishing to a
   partitioned broker (Kafka, SQS FIFO). All events for the same order go to the same partition,
   which preserves insertion order.

2. **Add a sequence number to each event.** Store a monotonically increasing `sequence` column in
   the outbox (incremented per-aggregate). Consumers reject any event whose sequence is less than
   or equal to the last sequence they have already processed for that aggregate.

3. **Optimistic locking on the consumer side.** Before applying an event, check that the
   aggregate's persisted version matches the event's expected version. If not, park the event and
   retry after a short delay, or route it to a resequencing buffer.

The most practical combination is (1) + (2): partition guarantees order within the broker, and
the sequence number lets consumers detect gaps and duplicates.

---

## Q4 — 10 Million Unprocessed Events

**Strategy:**

1. **Scale out workers horizontally with claim-based locking.**
   Each worker claims a batch atomically before processing it, preventing duplicate work:
   ```sql
   UPDATE outbox_events
      SET locked_by = $worker_id, locked_at = NOW()
    WHERE processed_at IS NULL
      AND (locked_at IS NULL OR locked_at < NOW() - INTERVAL '5 minutes')
    ORDER BY created_at
    LIMIT 500
   RETURNING *;
   ```
   The timeout on `locked_at` reclaims events from crashed workers.

2. **Increase batch size temporarily.** Tune the batch limit upward (e.g. 500–1000) while the
   backlog is draining, then return to a lower steady-state size.

3. **Prioritise recent events if ordering permits.** Old events may already be irrelevant to
   consumers. Process the last 24 hours first to restore live traffic, then drain the backlog
   from oldest to newest.

4. **Back-pressure and rate limiting.** Don't overwhelm the broker or downstream consumers.
   Implement a token-bucket or publish rate cap per worker.

5. **Dead-letter after N attempts.** Events that keep failing (e.g. payload consumers can no
   longer parse) should be moved to a `outbox_dead_letters` table after `attempts > threshold`
   so they don't block the queue.

6. **Archive or discard stale events with business approval.** If events older than a certain
   horizon have no meaningful effect (idempotency checks on the consumer would skip them anyway),
   bulk-mark them processed after confirming with the product owner.

7. **Monitor and alert.** Track `COUNT(*) WHERE processed_at IS NULL` as a metric. Alert when
   the backlog grows beyond a threshold so this situation is caught early next time.

---

## Q5 — The `time.Now()` Bug

**What is wrong:**

`time.Now()` returns the wall-clock time at the moment `OutboxMuts` is called, not the time the
business event actually occurred. This creates several problems:

- **Wrong semantics.** `created_at` in the outbox is supposed to represent *when the business
  fact happened*, not when the row was written. If the method is called milliseconds or seconds
  after `Order.Complete`, the recorded time drifts from the true event time.

- **Non-determinism in tests.** Any test that asserts `created_at` must use
  `time.AfterFunc`/`time.Sleep` hacks or tolerate loose comparisons. The system becomes harder
  to test correctly.

- **Clock skew across multiple events.** If `OutboxMuts` is called in a loop over several
  events, each call to `time.Now()` produces a slightly different value, so events that logically
  happened at the same instant get different timestamps.

**What it should be instead:**

The event already carries the authoritative logical timestamp (`CompletedAt` on
`OrderCompletedEvent`). Extract it from the event itself:

```go
createdAt := eventTimestamp(event) // returns e.CompletedAt for OrderCompletedEvent
```

This makes the outbox row's timestamp identical to the domain event's timestamp — deterministic,
testable, and semantically correct. The current implementation in `repo/outbox.go` already does
this correctly via `eventTimestamp`.
