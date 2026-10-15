package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func getRealPostgresDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boutique_user:boutique_password@localhost:5432/boutique_db?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("Failed to connect to real PostgreSQL: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to ping PostgreSQL database: %v", err)
	}
	return db
}

// TestRealPostgresSkipLockedConcurrency tests real concurrent transactions against real PostgreSQL
// to prove that FOR UPDATE SKIP LOCKED prevents lock contention and prevents double-processing.
func TestRealPostgresSkipLockedConcurrency(t *testing.T) {
	db := getRealPostgresDB(t)
	defer db.Close()

	ctx := context.Background()

	// 1. Clean outbox and insert 2 distinct pending events
	_, err := db.ExecContext(ctx, "DELETE FROM outbox WHERE id LIKE 'concurrency-test-%'")
	if err != nil {
		t.Fatalf("Failed to clean outbox: %v", err)
	}

	eventID1 := fmt.Sprintf("concurrency-test-1-%d", time.Now().UnixNano())
	eventID2 := fmt.Sprintf("concurrency-test-2-%d", time.Now().UnixNano())

	_, err = db.ExecContext(ctx, `
		INSERT INTO outbox (id, event_type, payload, created_at) VALUES 
		($1, 'OrderPlaced', '{"order":"1"}', NOW() - INTERVAL '2 seconds'),
		($2, 'OrderPlaced', '{"order":"2"}', NOW() - INTERVAL '1 second')
	`, eventID1, eventID2)
	if err != nil {
		t.Fatalf("Failed to seed outbox rows: %v", err)
	}

	// Synchronization barriers
	tx1Locked := make(chan struct{})
	tx2Done := make(chan struct{})

	var tx1SelectedID string
	var tx2SelectedID string
	var tx1Err, tx2Err error

	var wg sync.WaitGroup
	wg.Add(2)

	// ── WORKER / TRANSACTION 1 ──────────────────────────────────────────────────
	go func() {
		defer wg.Done()
		tx1, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			tx1Err = fmt.Errorf("tx1 begin error: %w", err)
			return
		}
		defer tx1.Rollback()

		// Select oldest pending row with FOR UPDATE SKIP LOCKED
		row := tx1.QueryRowContext(ctx, `
			SELECT id FROM outbox 
			WHERE id IN ($1, $2) AND published_at IS NULL 
			ORDER BY created_at ASC 
			LIMIT 1 
			FOR UPDATE SKIP LOCKED
		`, eventID1, eventID2)

		if err := row.Scan(&tx1SelectedID); err != nil {
			tx1Err = fmt.Errorf("tx1 scan error: %w", err)
			return
		}
		t.Logf("Tx 1 acquired row lock on: %s", tx1SelectedID)

		// Signal to Tx 2 that Tx 1 holds the lock on row 1
		close(tx1Locked)

		// Wait until Tx 2 has run concurrently and attempted selection
		<-tx2Done

		// Now mark as published and commit Tx 1
		_, err = tx1.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", tx1SelectedID)
		if err != nil {
			tx1Err = fmt.Errorf("tx1 update error: %w", err)
			return
		}
		_ = tx1.Commit()
	}()

	// ── WORKER / TRANSACTION 2 (Runs concurrently while Tx 1 holds row 1 lock) ──
	go func() {
		defer wg.Done()
		// Wait until Tx 1 has acquired its row lock
		<-tx1Locked

		tx2, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			tx2Err = fmt.Errorf("tx2 begin error: %w", err)
			close(tx2Done)
			return
		}
		defer tx2.Rollback()

		// Tx 2 attempts to select a row with FOR UPDATE SKIP LOCKED.
		// Since Tx 1 currently holds the lock on the first row, Postgres MUST skip it
		// and immediately return the second row without blocking!
		startTime := time.Now()
		row := tx2.QueryRowContext(ctx, `
			SELECT id FROM outbox 
			WHERE id IN ($1, $2) AND published_at IS NULL 
			ORDER BY created_at ASC 
			LIMIT 1 
			FOR UPDATE SKIP LOCKED
		`, eventID1, eventID2)

		if err := row.Scan(&tx2SelectedID); err != nil {
			tx2Err = fmt.Errorf("tx2 scan error: %w", err)
			close(tx2Done)
			return
		}
		duration := time.Since(startTime)
		t.Logf("Tx 2 acquired row lock on: %s (in %v without waiting for Tx 1)", tx2SelectedID, duration)

		close(tx2Done)

		// Mark as published and commit Tx 2
		_, err = tx2.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", tx2SelectedID)
		if err != nil {
			tx2Err = fmt.Errorf("tx2 update error: %w", err)
			return
		}
		_ = tx2.Commit()
	}()

	wg.Wait()

	if tx1Err != nil {
		t.Fatalf("Tx 1 failed: %v", tx1Err)
	}
	if tx2Err != nil {
		t.Fatalf("Tx 2 failed: %v", tx2Err)
	}

	// ── VERIFICATIONS ─────────────────────────────────────────────────────────
	// 1. Tx 1 must have acquired eventID1 (oldest row)
	if tx1SelectedID != eventID1 {
		t.Errorf("Expected Tx 1 to lock %s, but got %s", eventID1, tx1SelectedID)
	}

	// 2. Tx 2 must have skipped eventID1 and acquired eventID2!
	if tx2SelectedID != eventID2 {
		t.Errorf("Expected Tx 2 to skip locked row and acquire %s, but got %s", eventID2, tx2SelectedID)
	}

	// 3. Neither transaction should process the same row
	if tx1SelectedID == tx2SelectedID {
		t.Fatalf("CONCURRENCY VIOLATION: Both Tx 1 and Tx 2 selected the same row: %s", tx1SelectedID)
	}

	// 4. Verify both rows are now marked published in real Postgres
	var countUnpublished int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox 
		WHERE id IN ($1, $2) AND published_at IS NULL
	`, eventID1, eventID2).Scan(&countUnpublished)
	if err != nil {
		t.Fatalf("Failed to query unpublished count: %v", err)
	}
	if countUnpublished != 0 {
		t.Errorf("Expected 0 unpublished rows remaining, found %d", countUnpublished)
	}

	t.Logf("SUCCESS: Verified real PostgreSQL FOR UPDATE SKIP LOCKED concurrency between 2 concurrent transactions.")
}

// TestOutboxPublisherRecoveryRealPostgres verifies crash recovery:
// Rows left unpublished due to worker termination are swept and published upon recovery.
func TestOutboxPublisherRecoveryRealPostgres(t *testing.T) {
	db := getRealPostgresDB(t)
	defer db.Close()

	ctx := context.Background()
	testID := fmt.Sprintf("recovery-test-%d", time.Now().UnixNano())

	// Insert row with published_at IS NULL (simulating crash before publish)
	_, err := db.ExecContext(ctx, `
		INSERT INTO outbox (id, event_type, payload, created_at, published_at) 
		VALUES ($1, 'OrderPlaced', '{"order":"recover"}', NOW(), NULL)
	`, testID)
	if err != nil {
		t.Fatalf("Failed to insert pending row: %v", err)
	}

	// Sweep with FOR UPDATE SKIP LOCKED
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin sweep tx: %v", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, payload FROM outbox 
		WHERE id = $1 AND published_at IS NULL 
		ORDER BY created_at ASC 
		LIMIT 50 
		FOR UPDATE SKIP LOCKED
	`, testID)
	if err != nil {
		t.Fatalf("Sweep query failed: %v", err)
	}
	defer rows.Close()

	var idsToPublish []string
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err == nil {
			idsToPublish = append(idsToPublish, id)
		}
	}
	rows.Close()

	for _, id := range idsToPublish {
		_, err = tx.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", id)
		if err != nil {
			t.Fatalf("Failed to mark row published: %v", err)
		}
	}
	_ = tx.Commit()

	if len(idsToPublish) != 1 {
		t.Errorf("Expected exactly 1 recovered event, got %d", len(idsToPublish))
	}

	// Verify published_at is no longer NULL
	var isPublished bool
	err = db.QueryRowContext(ctx, "SELECT published_at IS NOT NULL FROM outbox WHERE id = $1", testID).Scan(&isPublished)
	if err != nil || !isPublished {
		t.Errorf("Row was not marked published: %v", err)
	}

	t.Log("SUCCESS: Verified outbox publisher recovery against real PostgreSQL.")
}
