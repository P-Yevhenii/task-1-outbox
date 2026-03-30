package repo

import (
	"encoding/json"
	"github.com/P-Yevhenii/task-1-outbox/contracts"
	"github.com/P-Yevhenii/task-1-outbox/domain"
	"time"

	"github.com/google/uuid"
)

// OutboxRepo builds INSERT mutations for the outbox_events table
type OutboxRepo struct{}

func NewOutboxRepo() *OutboxRepo {
	return &OutboxRepo{}
}

// MutsFromEvents produces one INSERT mutation per domain event raised on the order.
func (r *OutboxRepo) MutsFromEvents(order *domain.Order) []*contracts.Mutation {
	events := order.Events.Events()
	muts := make([]*contracts.Mutation, 0, len(events))

	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			// TODO: in prod need to wrap and propagate.
			panic("outbox: failed to marshal event: " + err.Error())
		}

		// Extract the logical timestamp from the event itself.
		createdAt := eventTimestamp(event)

		muts = append(muts, contracts.NewMutation(
			`INSERT INTO outbox_events (id, event_type, payload, created_at)
			 VALUES ($1, $2, $3, $4)`,
			uuid.New().String(),
			event.EventType(),
			payload,
			createdAt,
		))
	}

	return muts
}

// eventTimestamp extracts the logical time from a known event type.
// Each event carries its own timestamp — this avoids any dependency on time.Now().
func eventTimestamp(event domain.Event) time.Time {
	switch e := event.(type) {
	case domain.OrderCompletedEvent:
		return e.CompletedAt
	default:
		// TODO: Log a warning in production.
		return time.Time{}
	}
}
