package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/microservices-demo/src/checkoutservice/pkg/outbox"
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

type testSNSPublisher struct {
	mu        sync.Mutex
	published []string
}

func (p *testSNSPublisher) Publish(ctx context.Context, topicARN, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, message)
	return nil
}

// TestRealPostgresSkipLockedConcurrency tests real concurrent transactions against real PostgreSQL
// using production outbox.FetchAndLockPendingEvents with FOR UPDATE SKIP LOCKED.
func TestRealPostgresSkipLockedConcurrency(t *testing.T) {
	db := getRealPostgresDB(t)
	defer db.Close()

	ctx := context.Background()

	// 1. Clean outbox and insert 2 distinct pending events
	_, err := db.ExecContext(ctx, "DELETE FROM outbox WHERE published_at IS NULL")
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

	// ?? WORKER / TRANSACTION 1 ??????????????????????????????????????????????????
	go func() {
		defer wg.Done()
		tx1, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			tx1Err = fmt.Errorf("tx1 begin error: %w", err)
			return
		}
		defer tx1.Rollback()

		// Call production outbox package function with batchSize=1
		events, err := outbox.FetchAndLockPendingEvents(ctx, tx1, 1)
		if err != nil {
			tx1Err = fmt.Errorf("tx1 query error: %w", err)
			return
		}
		if len(events) > 0 {
			tx1SelectedID = events[0].ID
		}

		// Signal Tx 2 that Tx 1 holds the lock on its selected row
		close(tx1Locked)

		// Wait until Tx 2 has executed its query before Tx 1 commits
		select {
		case <-tx2Done:
		case <-time.After(5 * time.Second):
			tx1Err = fmt.Errorf("tx1 timed out waiting for tx2")
			return
		}

		// Mark row as published
		_, err = tx1.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", tx1SelectedID)
		if err != nil {
			tx1Err = fmt.Errorf("tx1 update error: %w", err)
			return
		}
		if err := tx1.Commit(); err != nil {
			tx1Err = fmt.Errorf("tx1 commit error: %w", err)
		}
	}()

	// ?? WORKER / TRANSACTION 2 ??????????????????????????????????????????????????
	go func() {
		defer wg.Done()
		// Wait until Tx 1 has already locked the oldest row
		select {
		case <-tx1Locked:
		case <-time.After(5 * time.Second):
			tx2Err = fmt.Errorf("tx2 timed out waiting for tx1 to lock")
			return
		}

		tx2, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			tx2Err = fmt.Errorf("tx2 begin error: %w", err)
			close(tx2Done)
			return
		}
		defer tx2.Rollback()

		start := time.Now()
		// Call production outbox package function with batchSize=1
		events, err := outbox.FetchAndLockPendingEvents(ctx, tx2, 1)
		elapsed := time.Since(start)
		if err != nil {
			tx2Err = fmt.Errorf("tx2 query error: %w", err)
			close(tx2Done)
			return
		}
		if len(events) > 0 {
			tx2SelectedID = events[0].ID
		}

		// SKIP LOCKED must return IMMEDIATELY without waiting on Tx 1's lock
		if elapsed > 100*time.Millisecond {
			tx2Err = fmt.Errorf("tx2 took %v to execute, which indicates blocking rather than SKIP LOCKED", elapsed)
		}

		// Mark row as published
		_, err = tx2.ExecContext(ctx, "UPDATE outbox SET published_at = NOW() WHERE id = $1", tx2SelectedID)
		if err != nil {
			tx2Err = fmt.Errorf("tx2 update error: %w", err)
			close(tx2Done)
			return
		}
		if err := tx2.Commit(); err != nil {
			tx2Err = fmt.Errorf("tx2 commit error: %w", err)
		}

		close(tx2Done)
	}()

	wg.Wait()

	if tx1Err != nil {
		t.Fatalf("Tx 1 failed: %v", tx1Err)
	}
	if tx2Err != nil {
		t.Fatalf("Tx 2 failed: %v", tx2Err)
	}

	// ?? VERIFICATIONS ?????????????????????????????????????????????????????????
	if tx1SelectedID != eventID1 {
		t.Errorf("Expected Tx 1 to lock %s, but got %s", eventID1, tx1SelectedID)
	}
	if tx2SelectedID != eventID2 {
		t.Errorf("Expected Tx 2 to skip locked row and acquire %s, but got %s", eventID2, tx2SelectedID)
	}
	if tx1SelectedID == tx2SelectedID {
		t.Fatalf("CONCURRENCY VIOLATION: Both Tx 1 and Tx 2 selected the same row: %s", tx1SelectedID)
	}

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

	t.Logf("SUCCESS: Verified production outbox.FetchAndLockPendingEvents concurrency against real PostgreSQL.")
}

// TestOutboxPublisherRecoveryRealPostgres verifies crash recovery using production outbox.PublishBatch
// and outbox.RecoverStaleEvents against real PostgreSQL.
func TestOutboxPublisherRecoveryRealPostgres(t *testing.T) {
	db := getRealPostgresDB(t)
	defer db.Close()

	ctx := context.Background()
	testID := fmt.Sprintf("recovery-test-%d", time.Now().UnixNano())

	// Insert row with published_at IS NULL (simulating crash before publish)
	_, err := db.ExecContext(ctx, `
		INSERT INTO outbox (id, event_type, payload, created_at, published_at) 
		VALUES ($1, 'OrderPlaced', '{"order":"recover"}', NOW() - INTERVAL '5 minutes', NULL)
	`, testID)
	if err != nil {
		t.Fatalf("Failed to insert pending row: %v", err)
	}

	// Verify outbox.RecoverStaleEvents finds this event
	staleCount, err := outbox.RecoverStaleEvents(ctx, db, 1*time.Minute)
	if err != nil {
		t.Fatalf("RecoverStaleEvents failed: %v", err)
	}
	if staleCount < 1 {
		t.Errorf("Expected at least 1 stale event, got %d", staleCount)
	}

	// Execute production outbox.PublishBatch
	pub := &testSNSPublisher{}
	published, err := outbox.PublishBatch(ctx, db, pub, "arn:aws:sns:il-central-1:123456789012:test-order-events", 50)
	if err != nil {
		t.Fatalf("PublishBatch failed: %v", err)
	}
	if published < 1 {
		t.Errorf("Expected at least 1 published event, got %d", published)
	}

	// Verify published_at is no longer NULL
	var isPublished bool
	err = db.QueryRowContext(ctx, "SELECT published_at IS NOT NULL FROM outbox WHERE id = $1", testID).Scan(&isPublished)
	if err != nil || !isPublished {
		t.Errorf("Row was not marked published: %v", err)
	}

	t.Log("SUCCESS: Verified production outbox.PublishBatch and RecoverStaleEvents against real PostgreSQL.")
}
