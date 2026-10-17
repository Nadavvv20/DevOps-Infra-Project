package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/microservices-demo/src/inventoryservice/pkg/inventory"
	"github.com/redis/go-redis/v9"
)

// TestAtomicHoldTransfer_ConcurrentReserveLastItem verifies:
// 1. Stock = 0 because guest holds the only item.
// 2. While transfer from guest to authenticated user happens, concurrent reservations by others FAIL.
// 3. Stock is never released to the wild during transfer (no release-then-reserve race).
// 4. Destination hold preserves remaining TTL, 60-min cap, and expiry ZSET score.
func TestAtomicHoldTransfer_ConcurrentReserveLastItem(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "TRANSFER_ITEM_TEST_1"
	guestCart := "guest-session-uuid-1111"
	userCart := "auth-user-sub-2222"

	stockKey := fmt.Sprintf("stock:{%s}", pid)
	guestHoldKey := fmt.Sprintf("hold:{%s}:%s", pid, guestCart)
	guestMaxKey := fmt.Sprintf("hold_max:{%s}:%s", pid, guestCart)
	userHoldKey := fmt.Sprintf("hold:{%s}:%s", pid, userCart)
	userMaxKey := fmt.Sprintf("hold_max:{%s}:%s", pid, userCart)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	// Clean up any test keys
	_ = rdb.Del(ctx, stockKey, guestHoldKey, guestMaxKey, userHoldKey, userMaxKey, expKey).Err()

	// 1. Seed stock = 1 and have guest reserve the only item
	_ = rdb.Set(ctx, stockKey, 1, 0).Err()

	now := time.Now().Unix()
	initialTTL := 900 // 15 minutes
	expiryTimestamp := now + int64(initialTTL)
	memberGuest := fmt.Sprintf("%s|%s|1", pid, guestCart)

	reserved, err := inventory.ReserveStock(ctx, rdb, pid, guestCart, 1, initialTTL, expiryTimestamp)
	if err != nil || !reserved {
		t.Fatalf("Failed to establish initial guest hold: err=%v reserved=%v", err, reserved)
	}

	// Set 60-minute absolute cap
	maxLifetime := now + 3600
	_ = rdb.Set(ctx, guestMaxKey, maxLifetime, 3600*time.Second).Err()

	// Verify stock is now 0
	stockVal, _ := rdb.Get(ctx, stockKey).Int64()
	if stockVal != 0 {
		t.Fatalf("Expected stock 0 after guest reservation, got %d", stockVal)
	}

	// 2. Launch 20 concurrent goroutines trying to steal/reserve the last item
	const concurrentAttempts = 20
	var competitorSuccesses int64
	var competitorFailures int64
	var transferSuccess int64

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	for i := 0; i < concurrentAttempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startBarrier

			competitorCart := fmt.Sprintf("competitor-cart-%d", idx)
			cMember := fmt.Sprintf("%s|%s|1", pid, competitorCart)
			ok, _ := inventory.ReserveStock(ctx, rdb, pid, competitorCart, 1, 900, time.Now().Unix()+900)
			if ok {
				atomic.AddInt64(&competitorSuccesses, 1)
			} else {
				atomic.AddInt64(&competitorFailures, 1)
			}
			_ = cMember
		}(i)
	}

	// Also launch the transfer simultaneously
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startBarrier

		transferred, err := inventory.TransferReservationHold(ctx, rdb, pid, guestCart, userCart)
		if err == nil && transferred {
			atomic.AddInt64(&transferSuccess, 1)
		}
	}()

	// Release all goroutines simultaneously
	close(startBarrier)
	wg.Wait()

	t.Logf("Concurrent Transfer Results: transferSuccess=%d, competitorSuccesses=%d, competitorFailures=%d",
		transferSuccess, competitorSuccesses, competitorFailures)

	if transferSuccess != 1 {
		t.Fatalf("Expected hold transfer to succeed (1), got %d", transferSuccess)
	}
	if competitorSuccesses != 0 {
		t.Fatalf("CRITICAL BUG: %d competitors successfully stole the item during transfer! Stock was leaked!", competitorSuccesses)
	}
	if competitorFailures != concurrentAttempts {
		t.Fatalf("Expected all %d competitor attempts to fail with out-of-stock, got %d", concurrentAttempts, competitorFailures)
	}

	// 3. Assert state after transfer
	// Guest hold must be DELeted
	existsGuest, _ := rdb.Exists(ctx, guestHoldKey).Result()
	if existsGuest != 0 {
		t.Errorf("Expected guest hold key to be deleted, but it still exists")
	}

	// User hold must exist with qty = 1
	userQty, err := rdb.Get(ctx, userHoldKey).Int64()
	if err != nil || userQty != 1 {
		t.Errorf("Expected user hold to have qty 1, got %d (err: %v)", userQty, err)
	}

	// Remaining TTL must be preserved (within 10s of initial 900s)
	ttl, err := rdb.TTL(ctx, userHoldKey).Result()
	if err != nil || ttl.Seconds() < 850 || ttl.Seconds() > 905 {
		t.Errorf("Expected preserved TTL ~900s, got %v (err: %v)", ttl, err)
	}

	// 60-min cap must exist on user key
	existsUserMax, _ := rdb.Exists(ctx, userMaxKey).Result()
	if existsUserMax == 0 {
		t.Errorf("Expected user hold_max key to exist, but it was not found")
	}

	// Expiry ZSET must have member for userCart with original score
	memberUser := fmt.Sprintf("%s|%s|1", pid, userCart)
	score, err := rdb.ZScore(ctx, expKey, memberUser).Result()
	if err != nil || int64(score) != expiryTimestamp {
		t.Errorf("Expected ZSET score %d, got %f (err: %v)", expiryTimestamp, score, err)
	}

	// Old member must be removed from ZSET
	_, errOld := rdb.ZScore(ctx, expKey, memberGuest).Result()
	if errOld == nil {
		t.Errorf("Expected old guest member to be removed from ZSET, but still found")
	}

	// Stock must still be 0
	finalStock, _ := rdb.Get(ctx, stockKey).Int64()
	if finalStock != 0 {
		t.Errorf("Expected stock 0, got %d", finalStock)
	}
}

// TestAtomicHoldTransfer_FailedTransferLeavesOriginalIntact verifies:
// 1. A transfer attempted on a nonexistent or expired hold fails cleanly.
// 2. If a transfer fails, the original hold and stock remain completely uncorrupted.
func TestAtomicHoldTransfer_FailedTransferLeavesOriginalIntact(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "TRANSFER_ITEM_TEST_2"
	validGuestCart := "guest-session-valid-3333"
	nonExistentCart := "non-existent-cart-4444"
	targetUser := "auth-user-5555"

	guestHoldKey := fmt.Sprintf("hold:{%s}:%s", pid, validGuestCart)
	guestMaxKey := fmt.Sprintf("hold_max:{%s}:%s", pid, validGuestCart)
	stockKey := fmt.Sprintf("stock:{%s}", pid)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	_ = rdb.Del(ctx, stockKey, guestHoldKey, guestMaxKey, expKey).Err()
	_ = rdb.Set(ctx, stockKey, 5, 0).Err()

	// Case A: Transfer nonexistent hold
	success, err := inventory.TransferReservationHold(ctx, rdb, pid, nonExistentCart, targetUser)
	if err != nil {
		t.Fatalf("Unexpected error on nonexistent hold transfer: %v", err)
	}
	if success {
		t.Fatalf("Expected transfer of nonexistent hold to return false, got true")
	}

	targetHoldKey := fmt.Sprintf("hold:{%s}:%s", pid, targetUser)
	existsTarget, _ := rdb.Exists(ctx, targetHoldKey).Result()
	if existsTarget != 0 {
		t.Errorf("Expected target hold to not exist after failed transfer")
	}

	// Case B: Valid hold intact after attempted transfer of a different/missing product
	reserved, err := inventory.ReserveStock(ctx, rdb, pid, validGuestCart, 2, 600, time.Now().Unix()+600)
	if err != nil || !reserved {
		t.Fatalf("Failed to establish guest hold: %v", err)
	}

	// Attempt transfer on WRONG product ID
	wrongPID := "WRONG_PRODUCT_ID"
	failedTransfer, err := inventory.TransferReservationHold(ctx, rdb, wrongPID, validGuestCart, targetUser)
	if err != nil || failedTransfer {
		t.Fatalf("Expected transfer on wrong PID to fail cleanly, got transferred=%v err=%v", failedTransfer, err)
	}

	// Original valid hold must remain completely untouched
	qty, err := rdb.Get(ctx, guestHoldKey).Int64()
	if err != nil || qty != 2 {
		t.Errorf("Expected original hold quantity 2 to remain untouched, got %d (err: %v)", qty, err)
	}

	stock, _ := rdb.Get(ctx, stockKey).Int64()
	if stock != 3 { // 5 - 2 = 3
		t.Errorf("Expected stock to remain 3, got %d", stock)
	}
}

// TestAtomicHoldTransfer_ReaperAndExtendedHold verifies:
// 1. A hold can be extended (e.g. cart touched), updating TTL and expiry score.
// 2. Transferring an extended hold moves it to the new user cart, retaining the extended score.
// 3. (a) Old member is gone and new member exists with the exact same score.
// 4. (b) Shortening the expiry makes the reaper restore stock exactly once (no double-restoration).
// 5. (c) The old cart ID can never trigger a restore.
func TestAtomicHoldTransfer_ReaperAndExtendedHold(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "TRANSFER_EXTEND_REAP_TEST"
	guestCart := "guest-extended-cart-111"
	userCart := "user-authenticated-cart-222"

	stockKey := inventory.StockKey(pid)
	guestHoldKey := inventory.HoldKey(pid, guestCart)
	guestMaxKey := inventory.HoldMaxKey(pid, guestCart)
	userHoldKey := inventory.HoldKey(pid, userCart)
	userMaxKey := inventory.HoldMaxKey(pid, userCart)
	expKey := inventory.ExpiryKey(pid)

	_ = rdb.Del(ctx, stockKey, guestHoldKey, guestMaxKey, userHoldKey, userMaxKey, expKey).Err()

	// 1. Initial stock = 10, guest reserves 3 units
	_ = rdb.Set(ctx, stockKey, 10, 0).Err()
	now := time.Now().Unix()
	initialTTL := 900
	initialExp := now + int64(initialTTL)
	guestMember := inventory.FormatExpiryMember(pid, guestCart, 3)

	reserved, err := inventory.ReserveStock(ctx, rdb, pid, guestCart, 3, initialTTL, initialExp)
	if err != nil || !reserved {
		t.Fatalf("Failed to establish guest hold: %v", err)
	}

	// Set 60-min cap key
	_ = rdb.Set(ctx, guestMaxKey, now+3600, 3600*time.Second).Err()

	// Stock is now 7
	stockVal, _ := rdb.Get(ctx, stockKey).Int64()
	if stockVal != 7 {
		t.Fatalf("Expected stock 7 after reservation, got %d", stockVal)
	}

	// 2. Simulate hold extension (e.g., user browsed cart, extending TTL by 300s)
	extendedTTL := 1200
	extendedExp := now + int64(extendedTTL)
	_ = rdb.Expire(ctx, guestHoldKey, time.Duration(extendedTTL)*time.Second).Err()
	_ = rdb.ZAdd(ctx, expKey, redis.Z{Score: float64(extendedExp), Member: guestMember}).Err()

	scoreBeforeTransfer, err := rdb.ZScore(ctx, expKey, guestMember).Result()
	if err != nil || int64(scoreBeforeTransfer) != extendedExp {
		t.Fatalf("Failed to verify extended hold score: %v (expected %d)", scoreBeforeTransfer, extendedExp)
	}

	// 3. Atomically transfer extended hold from guestCart to userCart
	transferred, err := inventory.TransferReservationHold(ctx, rdb, pid, guestCart, userCart)
	if err != nil || !transferred {
		t.Fatalf("Hold transfer failed: err=%v, transferred=%v", err, transferred)
	}

	// (a) Old member is GONE and new member exists with the exact same score
	_, oldErr := rdb.ZScore(ctx, expKey, guestMember).Result()
	if oldErr != redis.Nil {
		t.Fatalf("Assertion Failed: Old guest member %s still exists in %s!", guestMember, expKey)
	}

	userMember := inventory.FormatExpiryMember(pid, userCart, 3)
	userScore, newErr := rdb.ZScore(ctx, expKey, userMember).Result()
	if newErr != nil {
		t.Fatalf("Assertion Failed: New user member %s not found in %s: %v", userMember, expKey, newErr)
	}
	if int64(userScore) != extendedExp {
		t.Fatalf("Assertion Failed: New user member score mismatch: expected %d, got %f", extendedExp, userScore)
	}

	// (c) Verify old cart ID keys are gone; old cart ID can never trigger a restore
	existsGuestHold, _ := rdb.Exists(ctx, guestHoldKey).Result()
	existsGuestMax, _ := rdb.Exists(ctx, guestMaxKey).Result()
	if existsGuestHold != 0 || existsGuestMax != 0 {
		t.Fatalf("Assertion Failed: Old cart keys still exist in Redis after transfer")
	}

	// Verify user cart keys exist
	uQty, err := rdb.Get(ctx, userHoldKey).Int64()
	if err != nil || uQty != 3 {
		t.Fatalf("Expected user hold to have qty 3, got %d (err: %v)", uQty, err)
	}

	// (b) Shortening the expiry makes the reaper restore stock exactly once
	// Simulate expired hold by updating the score to 10 seconds in the past
	pastScore := float64(now - 10)
	_ = rdb.ZAdd(ctx, expKey, redis.Z{Score: pastScore, Member: userMember}).Err()

	// Run reaper at current time
	restoredCount, err := inventory.ReapExpiredHolds(ctx, rdb, now)
	if err != nil {
		t.Fatalf("ReapExpiredHolds failed: %v", err)
	}
	if restoredCount != 1 {
		t.Fatalf("Expected reaper to restore exactly 1 expired hold, got %d", restoredCount)
	}

	// Verify stock restored from 7 back to 10
	restoredStock, _ := rdb.Get(ctx, stockKey).Int64()
	if restoredStock != 10 {
		t.Fatalf("Expected stock restored to 10, got %d", restoredStock)
	}

	// Verify user hold key and member are completely removed
	existsUserHold, _ := rdb.Exists(ctx, userHoldKey).Result()
	if existsUserHold != 0 {
		t.Fatalf("Expected user hold key to be deleted by reaper, but still exists")
	}
	_, reapedErr := rdb.ZScore(ctx, expKey, userMember).Result()
	if reapedErr != redis.Nil {
		t.Fatalf("Expected user member to be removed from ZSET after reaper run")
	}

	// Run reaper AGAIN to verify idempotency (no double-restoration)
	restoredCountSecond, err := inventory.ReapExpiredHolds(ctx, rdb, now)
	if err != nil {
		t.Fatalf("Second ReapExpiredHolds failed: %v", err)
	}
	if restoredCountSecond != 0 {
		t.Fatalf("CRITICAL BUG: Double restoration! Reaper restored %d additional holds on second run", restoredCountSecond)
	}
	finalStock, _ := rdb.Get(ctx, stockKey).Int64()
	if finalStock != 10 {
		t.Fatalf("Expected stock to remain 10 after second reap run, got %d", finalStock)
	}
}
