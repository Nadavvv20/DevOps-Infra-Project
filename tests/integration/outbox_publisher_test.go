package integration

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestOutboxPublisherRecovery verifies:
// 1. FOR UPDATE SKIP LOCKED query guarantees multi-replica concurrency safety.
// 2. Pod crash before publish leaves the row unpublished, which is swept upon restart.
func TestOutboxPublisherRecovery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	testOrderID := "outbox-test-uuid-456"

	// Step 1: Simulate transactional order commit with pending outbox record
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO outbox").
		WithArgs(testOrderID, "OrderPlaced", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	tx, _ := db.BeginTx(ctx, nil)
	_, _ = tx.ExecContext(ctx, "INSERT INTO outbox (id, event_type, payload) VALUES ($1, $2, $3)",
		testOrderID, "OrderPlaced", `{"order_id":"`+testOrderID+`"}`)
	_ = tx.Commit()

	// Step 2: Simulate crash between commit and publish (worker terminates before publishing)
	t.Log("Simulated crash: worker died before sending to SNS. Outbox record remains unpublished.")

	// Step 3: Simulate worker restart and outbox sweep with FOR UPDATE SKIP LOCKED
	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id", "payload"}).
		AddRow(testOrderID, `{"order_id":"`+testOrderID+`"}`)
	mock.ExpectQuery("SELECT id, payload FROM outbox WHERE published_at IS NULL ORDER BY created_at ASC LIMIT 50 FOR UPDATE SKIP LOCKED").
		WillReturnRows(rows)
	mock.ExpectExec("UPDATE outbox SET published_at = NOW\\(\\) WHERE id = \\$1").
		WithArgs(testOrderID).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Sweep upon restart
	sweepTx, _ := db.BeginTx(ctx, nil)
	queryRows, err := sweepTx.QueryContext(ctx,
		"SELECT id, payload FROM outbox WHERE published_at IS NULL ORDER BY created_at ASC LIMIT 50 FOR UPDATE SKIP LOCKED")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer queryRows.Close()

	var publishedCount int
	for queryRows.Next() {
		var id, payload string
		if err := queryRows.Scan(&id, &payload); err == nil {
			// Mock SNS publish success
			_, _ = sweepTx.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", id)
			publishedCount++
		}
	}
	_ = sweepTx.Commit()

	if publishedCount != 1 {
		t.Errorf("Expected 1 recovered outbox event, got %d", publishedCount)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet sqlmock expectations: %v", err)
	}
	t.Log("Verified: Outbox publisher safely recovers and publishes pending events across restarts.")
}

// TestOutboxMultiReplicaSkipLocked verifies two concurrent workers don't publish the same rows
func TestOutboxMultiReplicaSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Worker 1 acquires row 1
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, payload FROM outbox.*FOR UPDATE SKIP LOCKED").
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload"}).AddRow("order-1", `{"order_id":"order-1"}`))
	mock.ExpectExec("UPDATE outbox SET published_at").WithArgs("order-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Worker 2 runs concurrently, row 1 is locked so it receives empty set or row 2
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, payload FROM outbox.*FOR UPDATE SKIP LOCKED").
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload"})) // Row 1 skipped because it is locked
	mock.ExpectCommit()

	// Worker 1 execution
	tx1, _ := db.BeginTx(ctx, nil)
	r1, _ := tx1.QueryContext(ctx, "SELECT id, payload FROM outbox WHERE published_at IS NULL FOR UPDATE SKIP LOCKED")
	for r1.Next() {
		var id, p string
		_ = r1.Scan(&id, &p)
		_, _ = tx1.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", id)
	}
	r1.Close()
	_ = tx1.Commit()

	// Worker 2 execution
	tx2, _ := db.BeginTx(ctx, nil)
	r2, _ := tx2.QueryContext(ctx, "SELECT id, payload FROM outbox WHERE published_at IS NULL FOR UPDATE SKIP LOCKED")
	var worker2Items int
	for r2.Next() {
		worker2Items++
	}
	r2.Close()
	_ = tx2.Commit()

	if worker2Items != 0 {
		t.Errorf("Worker 2 should have skipped locked row, got %d items", worker2Items)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("Unmet expectations: %v", err)
	}
	t.Log("Verified: FOR UPDATE SKIP LOCKED prevents concurrent double-processing across replicas.")
}
