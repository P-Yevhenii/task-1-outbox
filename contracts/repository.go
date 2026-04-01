package contracts

import (
	"context"
	"github.com/P-Yevhenii/task-1-outbox/domain"
)

type OrderRepository interface {
	Retrieve(ctx context.Context, id domain.OrderID) (*domain.Order, error)
	UpdateMut(order *domain.Order) *Mutation
	OutboxMuts(order *domain.Order) []*Mutation
}

// Mutation represents a single database write operation (INSERT or UPDATE).
type Mutation struct {
	Query string
	Args  []any
}

// NewMutation constructs a mutation descriptor.
func NewMutation(query string, args ...any) *Mutation {
	return &Mutation{Query: query, Args: args}
}

// Plan is an ordered collection of mutations to be executed
// inside a single database transaction.
type Plan struct {
	mutations []*Mutation
}

// NewPlan creates an empty plan.
func NewPlan() *Plan {
	return &Plan{}
}

func (p *Plan) Add(muts ...*Mutation) {
	p.mutations = append(p.mutations, muts...)
}

// Mutations returns the ordered list of mutations for use by the Committer.
func (p *Plan) Mutations() []*Mutation {
	return p.mutations
}
