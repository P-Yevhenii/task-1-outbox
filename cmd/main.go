package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/P-Yevhenii/task-1-outbox/contracts"
	"github.com/P-Yevhenii/task-1-outbox/domain"
	"github.com/P-Yevhenii/task-1-outbox/repo"
	"github.com/P-Yevhenii/task-1-outbox/usecases/complete_order"
	"github.com/P-Yevhenii/task-1-outbox/worker"
)

func main() {
	loadEnv(".env")

	dsn := envOrDefault("DATABASE_URL", "")
	orderID := envOrDefault("ORDER_ID", "")

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v\n\nhint: set DATABASE_URL or start postgres with:\n  docker run -d --name outbox-pg -e POSTGRES_USER=dev -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=outbox -p 5432:5432 postgres:16", err)
	}
	log.Println("connected to database")

	orderRepo := repo.NewOrderRepo(db)
	committer := &dbCommitter{db: db}
	clock := realClock{}
	interactor := complete_order.NewInteractor(orderRepo, committer, clock)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run the use case — complete the order and write the outbox entry atomically.
	resp, err := interactor.Execute(ctx, &complete_order.Request{
		OrderID: domain.OrderID(orderID),
	})
	if err != nil {
		log.Fatalf("complete order %s: %v", orderID, err)
	}
	log.Printf("order %s completed at %s", resp.OrderID, resp.CompletedAt.Format(time.RFC3339))

	// Start the outbox worker — reads unprocessed events and publishes them.
	store := worker.NewDBStore(db)
	pub := &logPublisher{}
	w := worker.NewOutboxWorker(
		store,
		pub,
		worker.WithBatchSize(50),
		worker.WithPollInterval(3*time.Second),
		worker.WithMaxAttempts(5),
	)

	log.Println("outbox worker started — press Ctrl+C to stop")
	if err := w.Run(ctx); err != nil {
		log.Printf("worker stopped: %v", err)
	}
}

// dbCommitter adapts contracts.Plan to the complete_order.Committer interface.
type dbCommitter struct{ db *sql.DB }

func (c *dbCommitter) Execute(ctx context.Context, plan *contracts.Plan) error {
	return plan.Execute(ctx, c.db)
}

// realClock returns the current wall-clock time.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// logPublisher writes events to stdout. Replace with a real broker client (Kafka, SQS, etc.).
type logPublisher struct{}

func (p *logPublisher) Publish(_ context.Context, eventType string, payload json.RawMessage) error {
	log.Printf("[broker] published  event_type=%-20s  payload=%s", eventType, payload)
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadEnv reads key=value pairs from path and sets them as environment variables.
// Already-set variables are not overwritten. Missing file is silently ignored.
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if os.Getenv(strings.TrimSpace(key)) == "" {
			os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value))
		}
	}
}
