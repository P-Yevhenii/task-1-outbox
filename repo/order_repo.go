package repo

import (
	"context"
	"database/sql"
	"errors"
	"github.com/P-Yevhenii/task-1-outbox/contracts"
	"github.com/P-Yevhenii/task-1-outbox/domain"
	"time"
)

// Compile-time check: OrderRepo must implement contracts.OrderRepository
var _ contracts.OrderRepository = (*OrderRepo)(nil)

// OrderRepo is the SQL implementation of the OrderRepository contract.
type OrderRepo struct {
	db     *sql.DB
	outbox *OutboxRepo
}

// NewOrderRepo constructs the repository.
func NewOrderRepo(db *sql.DB) *OrderRepo {
	return &OrderRepo{
		db:     db,
		outbox: NewOutboxRepo(),
	}
}

// Retrieve loads an Order aggregate by ID.
// If not found, returns domain.ErrOrderNotFound.
func (r *OrderRepo) Retrieve(ctx context.Context, id domain.OrderID) (*domain.Order, error) {
	const q = `
		SELECT id, customer_id, total_amount, status, completed_at
		FROM orders
		WHERE id = $1`

	row := r.db.QueryRowContext(ctx, q, string(id))

	var (
		rawID       string
		rawCustomer string
		totalAmount int64
		rawStatus   string
		completedAt sql.NullTime
	)

	if err := row.Scan(&rawID, &rawCustomer, &totalAmount, &rawStatus, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, err
	}

	var completedAtPtr *time.Time
	if completedAt.Valid {
		completedAtPtr = &completedAt.Time
	}

	return domain.Reconstruct(
		domain.OrderID(rawID),
		domain.CustomerID(rawCustomer),
		totalAmount,
		domain.OrderStatus(rawStatus),
		completedAtPtr,
	), nil
}

// UpdateMut returns a mutation that persists the order's tracked field changes.
func (r *OrderRepo) UpdateMut(order *domain.Order) *contracts.Mutation {
	return contracts.NewMutation(
		`UPDATE orders
		    SET status       = $1,
		        completed_at = $2
		  WHERE id = $3`,
		string(order.Status()),
		order.CompletedAt(),
		string(order.ID()),
	)
}

// OutboxMuts delegates outbox entry creation to OutboxRepo.
func (r *OrderRepo) OutboxMuts(order *domain.Order) []*contracts.Mutation {
	return r.outbox.MutsFromEvents(order)
}
