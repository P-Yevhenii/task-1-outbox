package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// recordingStore simulates Store.RunBatch without a real database.
type recordingStore struct {
	events []OutboxEvent
	err    error // if non-nil, RunBatch returns this immediately

	processedIDs []string
	failedIDs    []string
}

func (s *recordingStore) RunBatch(_ context.Context, _, _ int, process func(context.Context, OutboxEvent) error) error {
	if s.err != nil {
		return s.err
	}
	for _, e := range s.events {
		if err := process(context.Background(), e); err != nil {
			s.failedIDs = append(s.failedIDs, e.ID)
		} else {
			s.processedIDs = append(s.processedIDs, e.ID)
		}
	}
	return nil
}

// stubPublisher records every Publish call and optionally returns an error.
type stubPublisher struct {
	err       error
	published []publishCall
}

type publishCall struct {
	EventType string
	Payload   json.RawMessage
}

func (p *stubPublisher) Publish(_ context.Context, eventType string, payload json.RawMessage) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, publishCall{eventType, payload})
	return nil
}

// perEventPublisher lets individual events succeed or fail by event type.
type perEventPublisher struct {
	failTypes map[string]bool
	published []string
}

func (p *perEventPublisher) Publish(_ context.Context, eventType string, _ json.RawMessage) error {
	if p.failTypes[eventType] {
		return errors.New("broker error")
	}
	p.published = append(p.published, eventType)
	return nil
}

func makeEvent(id, eventType string) OutboxEvent {
	return OutboxEvent{
		ID:        id,
		EventType: eventType,
		Payload:   json.RawMessage(`{"id":"` + id + `"}`),
		Attempts:  0,
	}
}

func TestOutboxWorker_ProcessBatch_HappyPath(t *testing.T) {
	events := []OutboxEvent{
		makeEvent("evt-1", "order.completed"),
		makeEvent("evt-2", "order.completed"),
	}
	store := &recordingStore{events: events}
	pub := &stubPublisher{}

	w := NewOutboxWorker(store, pub)
	if err := w.processBatch(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.published) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(pub.published))
	}
	if len(store.processedIDs) != 2 {
		t.Errorf("expected 2 processed IDs, got %v", store.processedIDs)
	}
	if len(store.failedIDs) != 0 {
		t.Errorf("expected no failed IDs, got %v", store.failedIDs)
	}
}

func TestOutboxWorker_ProcessBatch_EmptyBatch(t *testing.T) {
	store := &recordingStore{events: nil}
	pub := &stubPublisher{}

	w := NewOutboxWorker(store, pub)
	if err := w.processBatch(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.published) != 0 {
		t.Errorf("expected no publish calls, got %d", len(pub.published))
	}
}

func TestOutboxWorker_ProcessBatch_AllPublishFail(t *testing.T) {
	events := []OutboxEvent{
		makeEvent("evt-1", "order.completed"),
		makeEvent("evt-2", "order.completed"),
	}
	store := &recordingStore{events: events}
	pub := &stubPublisher{err: errors.New("broker down")}

	w := NewOutboxWorker(store, pub)
	if err := w.processBatch(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(store.processedIDs) != 0 {
		t.Errorf("expected no processed events, got %v", store.processedIDs)
	}
	if len(store.failedIDs) != 2 {
		t.Errorf("expected 2 failed events, got %v", store.failedIDs)
	}
}

func TestOutboxWorker_ProcessBatch_PartialPublishFailure(t *testing.T) {
	events := []OutboxEvent{
		makeEvent("evt-1", "order.completed"),
		makeEvent("evt-2", "order.cancelled"), // will fail
		makeEvent("evt-3", "order.completed"),
	}
	store := &recordingStore{events: events}
	pub := &perEventPublisher{failTypes: map[string]bool{"order.cancelled": true}}

	w := NewOutboxWorker(store, pub)
	if err := w.processBatch(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pub.published) != 2 {
		t.Errorf("expected 2 published events, got %d", len(pub.published))
	}
	if len(store.processedIDs) != 2 {
		t.Errorf("expected 2 processed IDs, got %v", store.processedIDs)
	}
	if len(store.failedIDs) != 1 || store.failedIDs[0] != "evt-2" {
		t.Errorf("expected [evt-2] as failed, got %v", store.failedIDs)
	}
}

func TestOutboxWorker_ProcessBatch_StoreError(t *testing.T) {
	dbErr := errors.New("connection lost")
	store := &recordingStore{err: dbErr}
	pub := &stubPublisher{}

	w := NewOutboxWorker(store, pub)
	err := w.processBatch(context.Background())

	if !errors.Is(err, dbErr) {
		t.Errorf("expected store error, got %v", err)
	}
	if len(pub.published) != 0 {
		t.Errorf("expected no publish calls on store error")
	}
}

// Run must exit when the context is cancelled.
func TestOutboxWorker_Run_StopsOnContextCancel(t *testing.T) {
	store := &recordingStore{}
	pub := &stubPublisher{}

	w := NewOutboxWorker(store, pub, WithPollInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

// Run must keep polling after a transient store error, not exit.
func TestOutboxWorker_Run_ContinuesAfterTransientError(t *testing.T) {
	callCount := 0
	store := &countingStore{
		fn: func() error {
			callCount++
			if callCount == 1 {
				return errors.New("transient db error")
			}
			return nil
		},
	}
	pub := &stubPublisher{}

	ctx, cancel := context.WithCancel(context.Background())
	w := NewOutboxWorker(store, pub, WithPollInterval(10*time.Millisecond))

	done := make(chan struct{})
	go func() {
		w.Run(ctx) //nolint:errcheck
		close(done)
	}()

	// Wait until at least 2 batch cycles have run.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if callCount < 2 {
		t.Errorf("expected at least 2 RunBatch calls after a transient error, got %d", callCount)
	}
}

// countingStore calls fn on each RunBatch invocation.
type countingStore struct {
	fn func() error
}

func (s *countingStore) RunBatch(_ context.Context, _, _ int, _ func(context.Context, OutboxEvent) error) error {
	return s.fn()
}
