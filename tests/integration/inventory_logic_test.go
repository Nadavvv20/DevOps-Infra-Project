package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Redis Cluster mode safe Lua scripts
const reserveLuaScript = `
local stockKey = KEYS[1]
local holdKey  = KEYS[2]
local expKey   = KEYS[3]
local qty      = tonumber(ARGV[1])
local ttl      = tonumber(ARGV[2])
local expTime  = tonumber(ARGV[3])
local member   = ARGV[4]

local stock = tonumber(redis.call('GET', stockKey) or '0')
if stock >= qty then
    redis.call('DECRBY', stockKey, qty)
    redis.call('SET', holdKey, qty, 'EX', ttl)
    if expKey and expKey ~= '' then
        redis.call('ZADD', expKey, expTime, member)
    end
    return 1
else
    return 0
end
`

const reapProductLuaScript = `
local stockKey = KEYS[1]
local expKey   = KEYS[2]
local now      = tonumber(ARGV[1])
local expired  = redis.call('ZRANGEBYSCORE', expKey, '-inf', now, 'LIMIT', 0, 50)
local restored = 0

for _, item in ipairs(expired) do
    if redis.call('ZREM', expKey, item) == 1 then
        local pid, cart, qty = string.match(item, "([^|]+)|([^|]+)|([^|]+)")
        if qty then
            local holdKey = "hold:{" .. pid .. "}:" .. cart
            redis.call('INCRBY', stockKey, tonumber(qty))
            redis.call('DEL', holdKey)
            restored = restored + 1
        end
    end
end
return restored
`

const reconcileCASLuaScript = `
local current = redis.call('GET', KEYS[1])
if current == ARGV[1] then
    redis.call('SET', KEYS[1], ARGV[2])
    return 1
else
    return 0
end
`

func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err == nil {
		t.Logf("Running test against REAL Redis at %s", addr)
		cleanup := func() {
			rdb.Close()
		}
		return rdb, cleanup
	}

	// Fallback to miniredis if real Redis is not reachable
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cleanup := func() {
		rdb.Close()
		mr.Close()
	}
	return rdb, cleanup
}

// 1. TestLuaReservation verifies atomic stock decrement, hold key, and hash-tagged zset
func TestLuaReservation(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "OLJCESPC7Z"
	cartID := "cart-user-123"
	stockKey := fmt.Sprintf("stock:{%s}", pid)
	holdKey := fmt.Sprintf("hold:{%s}:%s", pid, cartID)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	// Clean test keys
	_ = rdb.Del(ctx, stockKey, holdKey, expKey).Err()

	// Initialize stock = 5
	_ = rdb.Set(ctx, stockKey, 5, 0).Err()

	now := time.Now().Unix()
	expTime := now + 900
	member := fmt.Sprintf("%s|%s|2", pid, cartID)

	// Run reserveLuaScript for 2 units
	res, err := rdb.Eval(ctx, reserveLuaScript, []string{stockKey, holdKey, expKey}, 2, 900, expTime, member).Result()
	if err != nil {
		t.Fatalf("Lua reservation failed: %v", err)
	}
	if res.(int64) != 1 {
		t.Fatalf("Expected reservation success (1), got %v", res)
	}

	// Verify stock decremented to 3
	stockVal, _ := rdb.Get(ctx, stockKey).Int64()
	if stockVal != 3 {
		t.Errorf("Expected stock 3, got %d", stockVal)
	}

	// Verify hold key holds qty = 2
	holdVal, _ := rdb.Get(ctx, holdKey).Int64()
	if holdVal != 2 {
		t.Errorf("Expected hold 2, got %d", holdVal)
	}

	// Verify member in expiry zset with correct score
	score, err := rdb.ZScore(ctx, expKey, member).Result()
	if err != nil || int64(score) != expTime {
		t.Errorf("Expected zset score %d, got %f (err: %v)", expTime, score, err)
	}
}

// 2. TestReaperMultiInstance verifies that 2 concurrent reaper workers restore stock exactly once
func TestReaperMultiInstance(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "66VCHSJNUP"
	cartID := "cart-exp-1"
	stockKey := fmt.Sprintf("stock:{%s}", pid)
	holdKey := fmt.Sprintf("hold:{%s}:%s", pid, cartID)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	_ = rdb.Del(ctx, stockKey, holdKey, expKey).Err()

	// Stock is 0 (all held)
	_ = rdb.Set(ctx, stockKey, 0, 0).Err()
	_ = rdb.Set(ctx, holdKey, 3, 0).Err()

	// Member expired 10 seconds ago
	pastTime := time.Now().Add(-10 * time.Second).Unix()
	member := fmt.Sprintf("%s|%s|3", pid, cartID)
	_ = rdb.ZAdd(ctx, expKey, redis.Z{Score: float64(pastTime), Member: member}).Err()

	now := time.Now().Unix()

	// Launch 2 concurrent reaper workers
	var totalRestored int64
	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier
			res, err := rdb.Eval(ctx, reapProductLuaScript, []string{stockKey, expKey}, now).Result()
			if err == nil {
				if restored, ok := res.(int64); ok {
					atomic.AddInt64(&totalRestored, restored)
				}
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	t.Logf("Reaper instances finished. Total items restored across instances: %d", totalRestored)

	// ACCEPTANCE CRITERIA: Exactly 1 restoration took place across 2 replicas
	if totalRestored != 1 {
		t.Errorf("Expected exactly 1 restoration event, got %d", totalRestored)
	}

	// Stock in Redis should now be 3 (restored once, never double-restored)
	finalStock, _ := rdb.Get(ctx, stockKey).Int64()
	if finalStock != 3 {
		t.Errorf("Stock double-restored or not restored: expected 3, got %d", finalStock)
	}
}

// 3. TestReconciliationCompareAndSetCAS verifies that reconciliation uses atomic Lua CAS
// to only overwrite Redis if current value equals the observed value.
func TestReconciliationCompareAndSetCAS(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "CAS-TEST-PID"
	stockKey := fmt.Sprintf("stock:{%s}", pid)

	// Clean key
	_ = rdb.Del(ctx, stockKey).Err()

	// Step 1: Initial stock in Redis = 10
	_ = rdb.Set(ctx, stockKey, 10, 0).Err()

	// Reconciler observes current value = 10 and expected corrected value = 8
	observedValue := "10"
	correctedExpectedValue := 8

	// Step 2: Concurrent reservation happens BEFORE reconciler applies correction!
	// A user reserves 1 unit, decreasing stock in Redis to 9
	_ = rdb.Set(ctx, stockKey, 9, 0).Err()

	// Step 3: Reconciler executes Lua CAS with old observed value ("10")
	res, err := rdb.Eval(ctx, reconcileCASLuaScript, []string{stockKey}, observedValue, correctedExpectedValue).Result()
	if err != nil {
		t.Fatalf("Lua CAS execution failed: %v", err)
	}

	// CAS MUST return 0 (aborted because current value 9 != observed value 10)
	if res.(int64) != 0 {
		t.Errorf("Expected CAS to abort (return 0) due to concurrent update, but got %v", res)
	}

	// Verify that stock was NOT overwritten with 8; it must remain 9!
	stockAfterAbort, _ := rdb.Get(ctx, stockKey).Int()
	if stockAfterAbort != 9 {
		t.Errorf("Concurrent modification was clobbered! Expected 9, got %d", stockAfterAbort)
	}
	t.Log("Verified: Lua CAS aborted safely upon concurrent modification without clobbering reservation.")

	// Step 4: Reconciler re-reads observed value = "9" and re-attempts CAS
	newObservedValue := "9"
	res2, err := rdb.Eval(ctx, reconcileCASLuaScript, []string{stockKey}, newObservedValue, correctedExpectedValue).Result()
	if err != nil {
		t.Fatalf("Second Lua CAS failed: %v", err)
	}

	// CAS MUST return 1 (success!)
	if res2.(int64) != 1 {
		t.Errorf("Expected CAS to succeed (return 1) when observed matches, but got %v", res2)
	}

	// Verify stock is now updated to 8
	finalStock, _ := rdb.Get(ctx, stockKey).Int()
	if finalStock != correctedExpectedValue {
		t.Errorf("Expected stock to be updated to %d, got %d", correctedExpectedValue, finalStock)
	}
	t.Log("SUCCESS: Verified Compare-And-Set (CAS) in Lua successfully prevents clobbering concurrent reservations.")
}

// 4. TestExtendReservation verifies hold TTL extension, hold_max, and 60-min absolute cap
func TestExtendReservation(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "L9ECAV7KIM"
	cartID := "user-cart-cap"
	holdKey := fmt.Sprintf("hold:{%s}:%s", pid, cartID)
	maxExpKey := fmt.Sprintf("hold_max:{%s}:%s", pid, cartID)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	_ = rdb.Del(ctx, holdKey, maxExpKey, expKey).Err()

	now := time.Now().Unix()
	initialMaxLifetime := now + 3600 // 60 min absolute cap

	// Create initial hold (15m) and absolute max key (60m)
	_ = rdb.Set(ctx, holdKey, 1, 15*time.Minute).Err()
	_ = rdb.Set(ctx, maxExpKey, initialMaxLifetime, 60*time.Minute).Err()
	member := fmt.Sprintf("%s|%s|1", pid, cartID)
	_ = rdb.ZAdd(ctx, expKey, redis.Z{Score: float64(now + 900), Member: member}).Err()

	extendHold := func(currentTime int64, reqExtendSec int64) (bool, int64) {
		maxExpVal, _ := rdb.Get(ctx, maxExpKey).Int64()
		if currentTime >= maxExpVal {
			return false, 0
		}
		remainingMax := maxExpVal - currentTime
		actualExtension := reqExtendSec
		if actualExtension > remainingMax {
			actualExtension = remainingMax
		}
		newExpiry := currentTime + actualExtension
		_ = rdb.Expire(ctx, holdKey, time.Duration(actualExtension)*time.Second).Err()
		_ = rdb.Expire(ctx, maxExpKey, time.Duration(remainingMax)*time.Second).Err()
		_ = rdb.ZAdd(ctx, expKey, redis.Z{Score: float64(newExpiry), Member: member}).Err()
		return true, actualExtension
	}

	// Normal extension
	ok, ext := extendHold(now+600, 900)
	if !ok || ext != 900 {
		t.Errorf("Expected normal extension of 900s, got ok=%v ext=%d", ok, ext)
	}

	// Extension at minute 55 (remaining cap is only 300s)
	okCap, extCap := extendHold(now+3300, 900)
	if !okCap || extCap != 300 {
		t.Errorf("Expected capped extension to 300s, got ok=%v ext=%d", okCap, extCap)
	}

	// Extension at minute 65 (past 60 min absolute cap)
	okRefused, _ := extendHold(now+3900, 900)
	if okRefused {
		t.Errorf("Expected extension beyond 60m absolute cap to be refused!")
	}
}

// 5. TestValidateReservation verifies reservation validity check before payment
func TestValidateReservation(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "2ZYFJ3GM2N"
	cartID := "cart-validate"
	holdKey := fmt.Sprintf("hold:{%s}:%s", pid, cartID)

	_ = rdb.Del(ctx, holdKey).Err()

	// Hold exists
	_ = rdb.Set(ctx, holdKey, 1, 10*time.Minute).Err()
	exists, _ := rdb.Exists(ctx, holdKey).Result()
	if exists != 1 {
		t.Errorf("Expected hold to be valid")
	}

	// Hold expired / deleted
	_ = rdb.Del(ctx, holdKey).Err()
	expiredExists, _ := rdb.Exists(ctx, holdKey).Result()
	if expiredExists != 0 {
		t.Errorf("Expected expired hold to be invalid")
	}
}

// 6. TestLastItemRace verifies 50 concurrent requests for 1 item:
// Exactly 1 succeeds, 49 fail with out-of-stock, rate-limit 429 is treated as failure, stock never negative.
func TestLastItemRace(t *testing.T) {
	rdb, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	pid := "OLJCESPC7Z"
	stockKey := fmt.Sprintf("stock:{%s}", pid)
	expKey := fmt.Sprintf("holds:expiry:{%s}", pid)

	_ = rdb.Del(ctx, stockKey, expKey).Err()

	// Exactly 1 item in stock
	_ = rdb.Set(ctx, stockKey, 1, 0).Err()

	const concurrentUsers = 50
	var successes int64
	var outOfStockFailures int64
	var rateLimitFailures int64

	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	for i := 0; i < concurrentUsers; i++ {
		wg.Add(1)
		go func(userIndex int) {
			defer wg.Done()
			<-startBarrier

			clientIP := fmt.Sprintf("10.0.1.%d", userIndex+1)

			rateKey := fmt.Sprintf("ratelimit:%s", clientIP)
			count, _ := rdb.Incr(ctx, rateKey).Result()
			if count > 10 {
				atomic.AddInt64(&rateLimitFailures, 1)
				return
			}

			userCart := fmt.Sprintf("cart-user-%d", userIndex)
			holdKey := fmt.Sprintf("hold:{%s}:%s", pid, userCart)
			member := fmt.Sprintf("%s|%s|1", pid, userCart)

			res, err := rdb.Eval(ctx, reserveLuaScript, []string{stockKey, holdKey, expKey}, 1, 900, time.Now().Unix()+900, member).Result()
			if err == nil && res.(int64) == 1 {
				atomic.AddInt64(&successes, 1)
			} else {
				atomic.AddInt64(&outOfStockFailures, 1)
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()

	t.Logf("Race Results: %d successes, %d out-of-stock, %d rate-limit-429s", successes, outOfStockFailures, rateLimitFailures)

	if rateLimitFailures > 0 {
		t.Fatalf("FAILED: %d requests tripped HTTP 429 rate limit during race test!", rateLimitFailures)
	}
	if successes != 1 {
		t.Fatalf("FAILED: Expected exactly 1 success, got %d", successes)
	}
	if outOfStockFailures != 49 {
		t.Fatalf("FAILED: Expected 49 out-of-stock failures, got %d", outOfStockFailures)
	}

	finalStock, _ := rdb.Get(ctx, stockKey).Int64()
	if finalStock != 0 {
		t.Errorf("Stock was not 0 after race: %d", finalStock)
	}
	if finalStock < 0 {
		t.Errorf("CRITICAL BUG: Stock is negative: %d", finalStock)
	}
}
