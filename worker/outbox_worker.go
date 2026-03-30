package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"time"
)

// OutboxEvent is a row fetched from the outbox_events table.
type OutboxEvent struct {
	ID        string
	EventType string
	Payload   json.RawMessage
	Attempts  int
}

// Publisher delivers a domain event payload to the message broker.
type Publisher interface {
	Publish(ctx context.Context, eventType string, payload json.RawMessage) error
}

// Store handles the transactional fetch-and-mark cycle.
type Store interface {
	RunBatch(ctx context.Context, batchSize, maxAttempts int, process func(context.Context, OutboxEvent) error) error
}

// OutboxWorker polls the outbox table and publishes pending events.
type OutboxWorker struct {
	store        Store
	publisher    Publisher
	batchSize    int
	pollInterval time.Duration
	maxAttempts  int
}

// Option configures an OutboxWorker.
type Option func(*OutboxWorker)

func WithBatchSize(n int) Option              { return func(w *OutboxWorker) { w.batchSize = n } }
func WithPollInterval(d time.Duration) Option { return func(w *OutboxWorker) { w.pollInterval = d } }
func WithMaxAttempts(n int) Option            { return func(w *OutboxWorker) { w.maxAttempts = n } }

// NewOutboxWorker constructs a worker with sensible defaults.
func NewOutboxWorker(store Store, publisher Publisher, opts ...Option) *OutboxWorker {
	w := &OutboxWorker{
		store:        store,
		publisher:    publisher,
		batchSize:    100,
		pollInterval: 5 * time.Second,
		maxAttempts:  5,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run starts the polling loop. It blocks until ctx is cancelled, then returns ctx.Err().
func (w *OutboxWorker) Run(ctx context.Context) error {
	for {
		if err := w.processBatch(ctx); err != nil && ctx.Err() == nil {
			// Log transient errors (DB blip, broker down) and keep polling.
			log.Printf("outbox worker: processBatch: %v", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.pollInterval):
		}
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context) error {
	return w.store.RunBatch(ctx, w.batchSize, w.maxAttempts, func(ctx context.Context, e OutboxEvent) error {
		return w.publisher.Publish(ctx, e.EventType, e.Payload)
	})
}

type DBStore struct {
	db *sql.DB
}

func NewDBStore(db *sql.DB) *DBStore { return &DBStore{db: db} }

// RunBatch implements Store.
func (s *DBStore) RunBatch(
	ctx context.Context,
	batchSize, maxAttempts int,
	process func(context.Context, OutboxEvent) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx, `
		SELECT id, event_type, payload, attempts
		  FROM outbox_events
		 WHERE processed_at IS NULL
		   AND attempts < $1
		 ORDER BY created_at
		 LIMIT $2
		   FOR UPDATE SKIP LOCKED`,
		maxAttempts, batchSize,
	)
	if err != nil {
		return err
	}

	var events []OutboxEvent
	for rows.Next() {
		var (
			e       OutboxEvent
			payload []byte
		)
		if err := rows.Scan(&e.ID, &e.EventType, &payload, &e.Attempts); err != nil {
			rows.Close()
			return err
		}
		e.Payload = json.RawMessage(payload)
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := time.Now().UTC()

	for _, e := range events {
		if err := process(ctx, e); err != nil {
			// Delivery failed: increment attempt counter, leave processed_at NULL.
			if _, dbErr := tx.ExecContext(ctx,
				`UPDATE outbox_events SET attempts = attempts + 1 WHERE id = $1`,
				e.ID,
			); dbErr != nil {
				return dbErr
			}
			log.Printf("outbox worker: publish failed for event %s (type=%s, attempt=%d): %v",
				e.ID, e.EventType, e.Attempts+1, err)
			continue
		}

		// Delivery succeeded: mark processed.
		if _, dbErr := tx.ExecContext(ctx,
			`UPDATE outbox_events SET processed_at = $1, attempts = attempts + 1 WHERE id = $2`,
			now, e.ID,
		); dbErr != nil {
			return dbErr
		}
	}

	return tx.Commit()
}
