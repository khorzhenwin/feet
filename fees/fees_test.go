package fees

import (
	"context"
	"testing"
	"time"
)

func TestAddLineItemUpdatesTotals(t *testing.T) {
	ctx := context.Background()
	resetDB(t, ctx)

	billID := newBillID(t)
	now := time.Now().UTC()
	insertBill(t, ctx, billID, BillOpen, now, now.Add(24*time.Hour))

	svc := &Service{}
	_, err := svc.AddLineItem(ctx, billID, &AddLineItemParams{
		Description: "setup fee",
		AmountMinor: 1000,
		Currency:    CurrencyUSD,
	})
	if err != nil {
		t.Fatalf("add line item failed: %v", err)
	}

	_, err = svc.AddLineItem(ctx, billID, &AddLineItemParams{
		Description: "gel fee",
		AmountMinor: 700,
		Currency:    CurrencyGEL,
	})
	if err != nil {
		t.Fatalf("add line item failed: %v", err)
	}

	bill, err := fetchBill(ctx, billID)
	if err != nil {
		t.Fatalf("fetch bill failed: %v", err)
	}
	if bill.LineItemCount != 2 {
		t.Fatalf("expected line item count 2, got %d", bill.LineItemCount)
	}
	if bill.TotalsByCurrency["USD"] != 1000 {
		t.Fatalf("expected USD total 1000, got %d", bill.TotalsByCurrency["USD"])
	}
	if bill.TotalsByCurrency["GEL"] != 700 {
		t.Fatalf("expected GEL total 700, got %d", bill.TotalsByCurrency["GEL"])
	}
}

func TestAddLineItemRejectsClosedBill(t *testing.T) {
	ctx := context.Background()
	resetDB(t, ctx)

	billID := newBillID(t)
	now := time.Now().UTC()
	insertBill(t, ctx, billID, BillClosed, now, now.Add(24*time.Hour))

	svc := &Service{}
	_, err := svc.AddLineItem(ctx, billID, &AddLineItemParams{
		Description: "late fee",
		AmountMinor: 100,
		Currency:    CurrencyUSD,
	})
	if err == nil {
		t.Fatalf("expected error when adding to closed bill")
	}
}

func TestCloseBillIdempotent(t *testing.T) {
	ctx := context.Background()
	resetDB(t, ctx)

	billID := newBillID(t)
	now := time.Now().UTC()
	insertBill(t, ctx, billID, BillOpen, now, now.Add(24*time.Hour))

	bill, alreadyClosed, err := closeBill(ctx, billID)
	if err != nil {
		t.Fatalf("close bill failed: %v", err)
	}
	if alreadyClosed {
		t.Fatalf("expected not already closed on first call")
	}
	if bill.Status != BillClosed {
		t.Fatalf("expected bill closed status")
	}

	bill, alreadyClosed, err = closeBill(ctx, billID)
	if err != nil {
		t.Fatalf("close bill failed: %v", err)
	}
	if !alreadyClosed {
		t.Fatalf("expected already closed on second call")
	}
	if bill.Status != BillClosed {
		t.Fatalf("expected bill closed status")
	}
}

func TestChargeBillFromOpen(t *testing.T) {
	ctx := context.Background()
	resetDB(t, ctx)

	billID := newBillID(t)
	now := time.Now().UTC()
	insertBill(t, ctx, billID, BillOpen, now, now.Add(24*time.Hour))

	bill, alreadyCharged, err := chargeBill(ctx, billID)
	if err != nil {
		t.Fatalf("charge bill failed: %v", err)
	}
	if alreadyCharged {
		t.Fatalf("expected not already charged on first call")
	}
	if bill.Status != BillCharged {
		t.Fatalf("expected bill charged status")
	}
	if bill.ClosedAt == nil || bill.ChargedAt == nil {
		t.Fatalf("expected closed_at and charged_at to be set")
	}
}

func TestIsUUID(t *testing.T) {
	valid := "a0e2015d-cb2b-49b3-b789-96bc2bc6124s"
	if isUUID(valid) {
		t.Fatalf("expected invalid UUID for %s", valid)
	}

	valid = "a0e2015d-cb2b-49b3-b789-96bc2bc61245"
	if !isUUID(valid) {
		t.Fatalf("expected valid UUID for %s", valid)
	}
}

func resetDB(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := billsDB.Exec(ctx, `DELETE FROM transaction_records`); err != nil {
		t.Fatalf("reset transaction_records: %v", err)
	}
	if _, err := billsDB.Exec(ctx, `DELETE FROM bills`); err != nil {
		t.Fatalf("reset bills: %v", err)
	}
}

func insertBill(t *testing.T, ctx context.Context, billID string, status BillStatus, periodStart, periodEnd time.Time) {
	t.Helper()
	now := time.Now().UTC()
	_, err := billsDB.Exec(ctx, `
		INSERT INTO bills
			(id, status, period_start, period_end, totals_by_currency,
			 line_item_count, metadata, workflow_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, billID, status, periodStart, periodEnd, []byte("{}"), 0, nil, "test-workflow", now, now)
	if err != nil {
		t.Fatalf("insert bill failed: %v", err)
	}
}

func newBillID(t *testing.T) string {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID failed: %v", err)
	}
	return id
}
