-- Orders table (source of truth for the order aggregate)
CREATE TABLE orders (
    id           UUID         PRIMARY KEY,
    customer_id  UUID         NOT NULL,
    total_amount BIGINT       NOT NULL,
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending',
    completed_at TIMESTAMPTZ  NULL
);

-- Outbox table: events are written here in the same transaction as the aggregate mutation.
-- A background worker reads pending rows, publishes them to the message broker,
-- and marks them processed.
CREATE TABLE outbox_events (
    id           UUID         PRIMARY KEY,           -- unique event ID; used as idempotency key by consumers
    event_type   VARCHAR(100) NOT NULL,              -- discriminator, e.g. "order.completed"
    payload      JSONB        NOT NULL,              -- full event serialised as JSON
    created_at   TIMESTAMPTZ  NOT NULL,              -- logical event time (from the domain event, not wall clock)
    processed_at TIMESTAMPTZ  NULL,                  -- NULL = pending; set by the worker after successful publish
    attempts     INTEGER      NOT NULL DEFAULT 0     -- delivery attempts; used for dead-lettering after threshold
);

-- Partial index used by the background worker to poll for unprocessed events in order.
-- Partial (WHERE processed_at IS NULL) keeps the index small as processed rows accumulate.
CREATE INDEX idx_outbox_pending ON outbox_events (created_at ASC)
    WHERE processed_at IS NULL;
