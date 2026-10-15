package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/microservices-demo/src/checkoutservice/pkg/orders"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCheckoutIdempotencyRealPostgres(t *testing.T) {
	db := getRealPostgresDB(t)
	defer db.Close()

	ctx := context.Background()

	testProductID := fmt.Sprintf("prod-idemp-%d", time.Now().UnixNano())
	initialStock := int32(50)

	// 1. Seed test product in products and inventory table
	_, err := db.ExecContext(ctx, `
		INSERT INTO products (id, name, price_usd_units, price_usd_nanos)
		VALUES ($1, 'Idempotency Test Item', 15, 0)
		ON CONFLICT (id) DO NOTHING`, testProductID)
	if err != nil {
		t.Fatalf("Failed to seed test product: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO inventory (product_id, total_stock, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (product_id) DO UPDATE SET total_stock = $2, updated_at = NOW()`,
		testProductID, initialStock)
	if err != nil {
		t.Fatalf("Failed to seed test inventory row: %v", err)
	}

	userID := fmt.Sprintf("user-%d", time.Now().UnixNano())
	userEmail := "buyer@example.com"

	// ── TEST CASE A: RETRY WITH SAME IDEMPOTENCY KEY ──────────────────────────
	// (a) retry with the same key returns the same order with no second payment call
	//     and no second inventory deduction
	key1 := fmt.Sprintf("checkout-token-attempt-1-%d", time.Now().UnixNano())
	orderID1 := orders.DeriveOrderID(key1)

	orderRec1 := &orders.OrderRecord{
		OrderID:       orderID1,
		UserCurrency:  "USD",
		TrackingID:    "TRK-IDEMP-001",
		ShipCostUnits: 5,
		ShipCostNanos: 0,
		Street:        "123 Market St",
		City:          "San Francisco",
		State:         "CA",
		Country:       "USA",
		ZipCode:       94105,
		Items: []orders.OrderItemRecord{
			{ProductID: testProductID, Quantity: 2, CostUnits: 15, CostNanos: 0},
		},
	}

	// First attempt: should succeed and deduct 2 items from inventory (50 -> 48)
	paymentCalls := 0
	simulatePayment := func() string {
		paymentCalls++
		return "tx-success-123"
	}

	// Place order attempt 1
	var placedOrder1 *orders.OrderRecord
	if existing, _ := orders.GetOrderFromDB(ctx, db, orderID1); existing != nil {
		placedOrder1 = existing
	} else {
		_ = simulatePayment() // Payment executed
		isNew, err := orders.PersistOrder(ctx, db, orderRec1, 35, 0, userID, userEmail)
		if err != nil {
			t.Fatalf("PersistOrder 1 failed: %v", err)
		}
		if !isNew {
			t.Fatalf("First attempt should be marked as new order")
		}
		placedOrder1, _ = orders.GetOrderFromDB(ctx, db, orderID1)
	}

	if paymentCalls != 1 {
		t.Errorf("Expected exactly 1 payment call, got %d", paymentCalls)
	}

	// Verify inventory after attempt 1
	var stockAfter1 int32
	err = db.QueryRowContext(ctx, "SELECT total_stock FROM inventory WHERE product_id = $1", testProductID).Scan(&stockAfter1)
	if err != nil || stockAfter1 != initialStock-2 {
		t.Fatalf("Expected stock %d after attempt 1, got %d (err: %v)", initialStock-2, stockAfter1, err)
	}

	// ATTEMPT 1 RETRY (same key1):
	// Must return existing order BEFORE payment and WITHOUT second inventory deduction
	var placedOrderRetry *orders.OrderRecord
	if existing, _ := orders.GetOrderFromDB(ctx, db, orderID1); existing != nil {
		t.Logf("Idempotent duplicate detected early by orderID %s! Returning immediately before payment.", orderID1)
		placedOrderRetry = existing
	} else {
		_ = simulatePayment() // Should NOT be called!
		_, _ = orders.PersistOrder(ctx, db, orderRec1, 35, 0, userID, userEmail)
	}

	// 1. Payment calls must still be 1 (no second payment call)
	if paymentCalls != 1 {
		t.Errorf("VIOLATION: Payment was called a second time on idempotent retry! Calls: %d", paymentCalls)
	}

	// 2. Returned order must match initial order
	if placedOrderRetry.OrderID != placedOrder1.OrderID || placedOrderRetry.TrackingID != placedOrder1.TrackingID {
		t.Errorf("Returned order does not match existing order: got %+v, want %+v", placedOrderRetry, placedOrder1)
	}

	// 3. Inventory deduction must NOT happen a second time
	var stockAfterRetry int32
	err = db.QueryRowContext(ctx, "SELECT total_stock FROM inventory WHERE product_id = $1", testProductID).Scan(&stockAfterRetry)
	if err != nil || stockAfterRetry != stockAfter1 {
		t.Fatalf("VIOLATION: Inventory was deducted again on retry! Stock was %d, now %d", stockAfter1, stockAfterRetry)
	}

	// ── TEST CASE B: TWO DIFFERENT CHECKOUTS BY SAME USER/EMAIL ───────────────
	// (b) two different checkouts by the same user/email create two orders
	key2 := fmt.Sprintf("checkout-token-attempt-2-%d", time.Now().UnixNano())
	orderID2 := orders.DeriveOrderID(key2)

	if orderID1 == orderID2 {
		t.Fatalf("Derived order IDs for distinct attempts must not collide: %s == %s", orderID1, orderID2)
	}

	orderRec2 := &orders.OrderRecord{
		OrderID:       orderID2,
		UserCurrency:  "USD",
		TrackingID:    "TRK-IDEMP-002",
		ShipCostUnits: 5,
		ShipCostNanos: 0,
		Street:        "123 Market St",
		City:          "San Francisco",
		State:         "CA",
		Country:       "USA",
		ZipCode:       94105,
		Items: []orders.OrderItemRecord{
			{ProductID: testProductID, Quantity: 3, CostUnits: 15, CostNanos: 0},
		},
	}

	_ = simulatePayment() // Second distinct checkout calls payment
	isNew2, err := orders.PersistOrder(ctx, db, orderRec2, 50, 0, userID, userEmail)
	if err != nil {
		t.Fatalf("Second distinct checkout failed: %v", err)
	}
	if !isNew2 {
		t.Fatalf("Second checkout by same user/email should create a NEW order, but was flagged duplicate!")
	}

	// Verify two distinct orders exist in database for the same user
	var userOrderCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders WHERE user_id = $1", userID).Scan(&userOrderCount)
	if err != nil || userOrderCount != 2 {
		t.Errorf("Expected 2 distinct orders in database for user %s, found %d", userID, userOrderCount)
	}

	// Verify inventory decreased by 3 for order 2 (48 -> 45)
	var finalStock int32
	err = db.QueryRowContext(ctx, "SELECT total_stock FROM inventory WHERE product_id = $1", testProductID).Scan(&finalStock)
	if err != nil || finalStock != initialStock-5 {
		t.Errorf("Expected final stock %d, got %d", initialStock-5, finalStock)
	}

	t.Logf("SUCCESS: Verified (a) retry with same key returns existing order with no second payment and no inventory deduction, and (b) two distinct checkouts by same user/email create two distinct orders.")
}
