package complete_order

import (
	"context"
	"github.com/P-Yevhenii/task-1-outbox/contracts"
	"github.com/P-Yevhenii/task-1-outbox/domain"
	"time"
)

// Clock abstracts time so usecases are deterministic and testable.
type Clock interface {
	Now() time.Time
}

// Committer executes a Plan atomically inside a DB transaction.
type Committer interface {
	Execute(ctx context.Context, plan *contracts.Plan) error
}

// Request carries the input data for this usecase.
type Request struct {
	OrderID domain.OrderID
}

// Response carries the output of a successful execution.
type Response struct {
	OrderID     domain.OrderID
	CompletedAt time.Time
}

// Interactor orchestrates the "complete order" use case.
type Interactor struct {
	repo      contracts.OrderRepository
	committer Committer
	clock     Clock
}

// NewInteractor constructs the usecase with its dependencies.
func NewInteractor(
	repo contracts.OrderRepository,
	committer Committer,
	clock Clock,
) *Interactor {
	return &Interactor{
		repo:      repo,
		committer: committer,
		clock:     clock,
	}
}

// Execute runs the complete-order business flow.
func (uc *Interactor) Execute(ctx context.Context, req *Request) (*Response, error) {
	order, err := uc.repo.Retrieve(ctx, req.OrderID)
	if err != nil {
		return nil, err
	}

	// validates transition, tracks changes, raises event
	if err := order.Complete(uc.clock.Now()); err != nil {
		return nil, err
	}

	// collect ALL mutations into one plan
	plan := contracts.NewPlan()
	plan.Add(uc.repo.UpdateMut(order))     // order row update
	plan.Add(uc.repo.OutboxMuts(order)...) // outbox event rows

	// both writes succeed or neither does
	if err := uc.committer.Execute(ctx, plan); err != nil {
		return nil, err
	}

	return &Response{
		OrderID:     order.ID(),
		CompletedAt: *order.CompletedAt(),
	}, nil
}
