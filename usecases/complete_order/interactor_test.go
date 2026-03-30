package complete_order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/P-Yevhenii/task-1-outbox/contracts"
	"github.com/P-Yevhenii/task-1-outbox/domain"
)

type stubClock struct{ t time.Time }

func (c *stubClock) Now() time.Time { return c.t }

type stubCommitter struct{ err error }

func (c *stubCommitter) Execute(_ context.Context, _ *contracts.Plan) error { return c.err }

type stubRepo struct {
	order *domain.Order
	err   error

	updateMutCalled  bool
	outboxMutsCalled bool
}

func (r *stubRepo) Retrieve(_ context.Context, _ domain.OrderID) (*domain.Order, error) {
	return r.order, r.err
}

func (r *stubRepo) UpdateMut(_ *domain.Order) *contracts.Mutation {
	r.updateMutCalled = true
	return contracts.NewMutation("UPDATE orders SET status=$1 WHERE id=$2", "completed", "order-1")
}

func (r *stubRepo) OutboxMuts(_ *domain.Order) []*contracts.Mutation {
	r.outboxMutsCalled = true
	return []*contracts.Mutation{
		contracts.NewMutation("INSERT INTO outbox_events VALUES ($1)", "evt-1"),
	}
}

func activeOrder() *domain.Order {
	return domain.Reconstruct("order-1", "customer-1", 10_000, domain.StatusActive, nil)
}

func completedOrder() *domain.Order {
	t := time.Now()
	return domain.Reconstruct("order-1", "customer-1", 10_000, domain.StatusCompleted, &t)
}

func pendingOrder() *domain.Order {
	return domain.Reconstruct("order-1", "customer-1", 10_000, domain.StatusPending, nil)
}

func TestInteractor_Execute_HappyPath(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	repo := &stubRepo{order: activeOrder()}
	committer := &stubCommitter{}
	clock := &stubClock{t: now}

	uc := NewInteractor(repo, committer, clock)
	resp, err := uc.Execute(context.Background(), &Request{OrderID: "order-1"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.OrderID != "order-1" {
		t.Errorf("OrderID: want order-1, got %s", resp.OrderID)
	}
	if !resp.CompletedAt.Equal(now) {
		t.Errorf("CompletedAt: want %v, got %v", now, resp.CompletedAt)
	}
}

func TestInteractor_Execute_BuildsPlanWithAllMutations(t *testing.T) {
	repo := &stubRepo{order: activeOrder()}
	committer := &stubCommitter{}

	uc := NewInteractor(repo, committer, &stubClock{t: time.Now()})
	_, err := uc.Execute(context.Background(), &Request{OrderID: "order-1"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.updateMutCalled {
		t.Error("UpdateMut was not called: order mutation missing from plan")
	}
	if !repo.outboxMutsCalled {
		t.Error("OutboxMuts was not called: outbox mutation missing from plan")
	}
}

func TestInteractor_Execute_OrderNotFound(t *testing.T) {
	repo := &stubRepo{err: domain.ErrOrderNotFound}

	uc := NewInteractor(repo, &stubCommitter{}, &stubClock{})
	_, err := uc.Execute(context.Background(), &Request{OrderID: "missing"})

	if !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("want ErrOrderNotFound, got %v", err)
	}
}

func TestInteractor_Execute_AlreadyCompleted(t *testing.T) {
	repo := &stubRepo{order: completedOrder()}

	uc := NewInteractor(repo, &stubCommitter{}, &stubClock{})
	_, err := uc.Execute(context.Background(), &Request{OrderID: "order-1"})

	if !errors.Is(err, domain.ErrOrderAlreadyCompleted) {
		t.Errorf("want ErrOrderAlreadyCompleted, got %v", err)
	}
}

func TestInteractor_Execute_InvalidStatusTransition(t *testing.T) {
	repo := &stubRepo{order: pendingOrder()}

	uc := NewInteractor(repo, &stubCommitter{}, &stubClock{})
	_, err := uc.Execute(context.Background(), &Request{OrderID: "order-1"})

	if !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Errorf("want ErrInvalidStatusTransition, got %v", err)
	}
}

func TestInteractor_Execute_CommitterError_ReturnsError(t *testing.T) {
	dbErr := errors.New("connection lost")
	repo := &stubRepo{order: activeOrder()}
	committer := &stubCommitter{err: dbErr}

	uc := NewInteractor(repo, committer, &stubClock{t: time.Now()})
	_, err := uc.Execute(context.Background(), &Request{OrderID: "order-1"})

	if !errors.Is(err, dbErr) {
		t.Errorf("want db error, got %v", err)
	}
}
