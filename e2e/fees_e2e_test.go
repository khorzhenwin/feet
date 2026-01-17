package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

type billStatus string

const (
	billOpen   billStatus = "open"
	billClosed billStatus = "closed"
)

type createBillResponse struct {
	Bill struct {
		ID      string     `json:"id"`
		Status  billStatus `json:"status"`
		PeriodEnd time.Time `json:"period_end"`
	} `json:"bill"`
}

type addLineItemResponse struct {
	TotalsByCurrency map[string]int64 `json:"totals_by_currency"`
}

type getBillResponse struct {
	ID               string            `json:"id"`
	Status           billStatus        `json:"status"`
	TotalsByCurrency map[string]int64  `json:"totals_by_currency"`
}

func TestManualCloseFlow(t *testing.T) {
	baseURL := getBaseURL(t)

	billID := createBill(t, baseURL, nil)
	addLineItem(t, baseURL, billID, "setup fee", 1000, "USD")
	addLineItem(t, baseURL, billID, "support", 2500, "USD")

	closeBill(t, baseURL, billID)
	bill := getBill(t, baseURL, billID)
	if bill.Status != billClosed {
		t.Fatalf("expected closed bill, got %s", bill.Status)
	}

	resp, err := doJSON(context.Background(), http.MethodPost, baseURL+"/bills/"+billID+"/line-items", map[string]interface{}{
		"description":  "late fee",
		"amount_minor": 100,
		"currency":     "USD",
	})
	if err != nil {
		t.Fatalf("add line item after close failed: %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected rejection when adding to closed bill")
	}
}

func TestAutoCloseFlow(t *testing.T) {
	baseURL := getBaseURL(t)

	periodEnd := time.Now().UTC().Add(5 * time.Second)
	billID := createBill(t, baseURL, map[string]interface{}{
		"period_end": periodEnd,
	})

	waitUntil(t, 12*time.Second, func() bool {
		bill := getBill(t, baseURL, billID)
		return bill.Status == billClosed
	})
}

func TestMultiCurrencyTotals(t *testing.T) {
	baseURL := getBaseURL(t)

	billID := createBill(t, baseURL, nil)
	addLineItem(t, baseURL, billID, "usd fee", 500, "USD")
	addLineItem(t, baseURL, billID, "gel fee", 700, "GEL")

	bill := getBill(t, baseURL, billID)
	if bill.TotalsByCurrency["USD"] != 500 {
		t.Fatalf("expected USD total 500, got %d", bill.TotalsByCurrency["USD"])
	}
	if bill.TotalsByCurrency["GEL"] != 700 {
		t.Fatalf("expected GEL total 700, got %d", bill.TotalsByCurrency["GEL"])
	}
}

func getBaseURL(t *testing.T) string {
	t.Helper()
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("E2E_BASE_URL not set")
	}
	return baseURL
}

func createBill(t *testing.T, baseURL string, body map[string]interface{}) string {
	t.Helper()
	resp, err := doJSON(context.Background(), http.MethodPost, baseURL+"/bills", body)
	if err != nil {
		t.Fatalf("create bill failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create bill status %d", resp.StatusCode)
	}

	var payload createBillResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode create bill response: %v", err)
	}
	if payload.Bill.ID == "" {
		t.Fatal("missing bill id")
	}
	return payload.Bill.ID
}

func addLineItem(t *testing.T, baseURL, billID, description string, amountMinor int64, currency string) {
	t.Helper()
	resp, err := doJSON(context.Background(), http.MethodPost, baseURL+"/bills/"+billID+"/line-items", map[string]interface{}{
		"description":  description,
		"amount_minor": amountMinor,
		"currency":     currency,
	})
	if err != nil {
		t.Fatalf("add line item failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add line item status %d", resp.StatusCode)
	}
}

func closeBill(t *testing.T, baseURL, billID string) {
	t.Helper()
	resp, err := doJSON(context.Background(), http.MethodPost, baseURL+"/bills/"+billID+"/close", nil)
	if err != nil {
		t.Fatalf("close bill failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close bill status %d", resp.StatusCode)
	}
}

func getBill(t *testing.T, baseURL, billID string) getBillResponse {
	t.Helper()
	resp, err := doJSON(context.Background(), http.MethodGet, baseURL+"/bills/"+billID, nil)
	if err != nil {
		t.Fatalf("get bill failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get bill status %d", resp.StatusCode)
	}

	var bill getBillResponse
	if err := json.NewDecoder(resp.Body).Decode(&bill); err != nil {
		t.Fatalf("decode bill response: %v", err)
	}
	return bill
}

func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func doJSON(ctx context.Context, method, url string, body interface{}) (*http.Response, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}
