package fees

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

var billsDB = sqldb.NewDatabase("fees", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

const (
	taskQueue       = "fees-billing"
	closeSignalName = "bill-closed"
)

//encore:service
type Service struct {
	temporal client.Client
	worker   worker.Worker
	config   serviceConfig
}

type serviceConfig struct {
	temporalAddress   string
	temporalNamespace string
	feePeriod         time.Duration
}

func initService() (*Service, error) {
	cfg := loadConfig()
	c, err := dialTemporalWithRetry(cfg.temporalAddress, cfg.temporalNamespace, 10, 3*time.Second)
	if err != nil {
		return nil, err
	}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(BillingPeriodWorkflow)
	w.RegisterActivity(CreateBillActivity)
	w.RegisterActivity(CloseBillActivity)
	if err := w.Start(); err != nil {
		c.Close()
		return nil, err
	}

	return &Service{
		temporal: c,
		worker:   w,
		config:   cfg,
	}, nil
}

func dialTemporalWithRetry(address, namespace string, attempts int, delay time.Duration) (client.Client, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		c, err := client.Dial(client.Options{
			HostPort:  address,
			Namespace: namespace,
		})
		if err == nil {
			return c, nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	return nil, lastErr
}

func loadConfig() serviceConfig {
	address := os.Getenv("TEMPORAL_ADDRESS")
	if address == "" {
		address = "localhost:7233"
	}

	namespace := os.Getenv("TEMPORAL_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	feePeriod := 30 * 24 * time.Hour
	if raw := os.Getenv("FEE_PERIOD"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			feePeriod = d
		} else {
			log.Printf("fees: invalid FEE_PERIOD %q, using default", raw)
		}
	}

	return serviceConfig{
		temporalAddress:   address,
		temporalNamespace: namespace,
		feePeriod:         feePeriod,
	}
}

func (s *Service) Shutdown(force context.Context) {
	if s.worker != nil {
		s.worker.Stop()
	}
	if s.temporal != nil {
		s.temporal.Close()
	}
}

type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyGEL Currency = "GEL"
)

var supportedCurrencies = map[Currency]struct{}{
	CurrencyUSD: {},
	CurrencyGEL: {},
}

type BillStatus string

const (
	BillOpen    BillStatus = "open"
	BillClosed  BillStatus = "closed"
	BillCharged BillStatus = "charged"
)

type Bill struct {
	ID               string           `json:"id"`
	Status           BillStatus       `json:"status"`
	PeriodStart      time.Time        `json:"period_start"`
	PeriodEnd        time.Time        `json:"period_end"`
	ClosedAt         *time.Time       `json:"closed_at,omitempty"`
	ChargedAt        *time.Time       `json:"charged_at,omitempty"`
	TotalsByCurrency map[string]int64 `json:"totals_by_currency"`
	LineItemCount    int              `json:"line_item_count"`
	Metadata         json.RawMessage  `json:"metadata,omitempty"`
	WorkflowID       string           `json:"workflow_id"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type TransactionRecord struct {
	ID          string          `json:"id"`
	BillID      string          `json:"bill_id"`
	Description string          `json:"description"`
	AmountMinor int64           `json:"amount_minor"`
	Currency    Currency        `json:"currency"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CreateBillParams struct {
	PeriodStart *time.Time      `json:"period_start,omitempty"`
	PeriodEnd   *time.Time      `json:"period_end,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

type CreateBillResponse struct {
	Bill       Bill   `json:"bill"`
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
}

//encore:api public method=POST path=/bills
func (s *Service) CreateBill(ctx context.Context, p *CreateBillParams) (*CreateBillResponse, error) {
	now := time.Now().UTC()
	periodStart := now
	if p != nil && p.PeriodStart != nil {
		periodStart = p.PeriodStart.UTC()
	}

	periodEnd := periodStart.Add(s.config.feePeriod)
	if p != nil && p.PeriodEnd != nil {
		periodEnd = p.PeriodEnd.UTC()
	}

	billID, err := newUUID()
	if err != nil {
		return nil, errs.Wrap(err, "generate bill id")
	}
	workflowID := fmt.Sprintf("bill-%s", billID)

	var metadata json.RawMessage
	if p != nil {
		metadata = p.Metadata
	}

	input := BillingWorkflowInput{
		BillID:      billID,
		WorkflowID:  workflowID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Metadata:    metadata,
	}

	if err := insertBillRecord(ctx, input); err != nil {
		return nil, errs.Wrap(err, "create bill")
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, BillingPeriodWorkflow, input)
	if err != nil {
		_, _ = billsDB.Exec(ctx, `DELETE FROM bills WHERE id = $1`, billID)
		return nil, errs.Wrap(err, "start billing workflow")
	}

	bill := Bill{
		ID:               billID,
		Status:           BillOpen,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
		TotalsByCurrency: map[string]int64{},
		LineItemCount:    0,
		Metadata:         metadata,
		WorkflowID:       workflowID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	return &CreateBillResponse{
		Bill:       bill,
		WorkflowID: workflowID,
		RunID:      run.GetRunID(),
	}, nil
}

type AddLineItemParams struct {
	Description string          `json:"description"`
	AmountMinor int64           `json:"amount_minor"`
	Currency    Currency        `json:"currency"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

type AddLineItemResponse struct {
	LineItem         TransactionRecord `json:"line_item"`
	TotalsByCurrency map[string]int64  `json:"totals_by_currency"`
}

//encore:api public method=POST path=/bills/:bill_id/line-items
func (s *Service) AddLineItem(ctx context.Context, bill_id string, p *AddLineItemParams) (*AddLineItemResponse, error) {
	if p == nil {
		return nil, badRequest("missing request body")
	}
	if p.AmountMinor < 0 {
		return nil, badRequest("amount_minor must be non-negative")
	}
	if _, ok := supportedCurrencies[p.Currency]; !ok {
		return nil, badRequest("unsupported currency")
	}
	if p.Description == "" {
		return nil, badRequest("description is required")
	}

	tx, err := billsDB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var status BillStatus
	var totalsRaw []byte
	var lineItemCount int
	err = tx.QueryRow(ctx, `
		SELECT status, totals_by_currency, line_item_count
		FROM bills
		WHERE id = $1
		FOR UPDATE
	`, bill_id).Scan(&status, &totalsRaw, &lineItemCount)
	if err != nil {
		if errors.Is(err, sqldb.ErrNoRows) {
			return nil, notFound("bill not found")
		}
		return nil, err
	}
	if status != BillOpen {
		return nil, badRequest("bill is not open")
	}

	totals := map[string]int64{}
	if len(totalsRaw) > 0 {
		if err := json.Unmarshal(totalsRaw, &totals); err != nil {
			return nil, err
		}
	}
	totals[string(p.Currency)] += p.AmountMinor

	lineItemID, err := newUUID()
	if err != nil {
		return nil, errs.Wrap(err, "generate line item id")
	}
	now := time.Now().UTC()

	_, err = tx.Exec(ctx, `
		INSERT INTO transaction_records
			(id, bill_id, description, amount_minor, currency, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, lineItemID, bill_id, p.Description, p.AmountMinor, p.Currency, p.Metadata, now)
	if err != nil {
		return nil, err
	}

	totalsJSON, err := json.Marshal(totals)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE bills
		SET totals_by_currency = $1,
			line_item_count = $2,
			updated_at = $3
		WHERE id = $4
	`, totalsJSON, lineItemCount+1, now, bill_id)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &AddLineItemResponse{
		LineItem: TransactionRecord{
			ID:          lineItemID,
			BillID:      bill_id,
			Description: p.Description,
			AmountMinor: p.AmountMinor,
			Currency:    p.Currency,
			Metadata:    p.Metadata,
			CreatedAt:   now,
		},
		TotalsByCurrency: totals,
	}, nil
}

type CloseBillResponse struct {
	Bill Bill `json:"bill"`
}

//encore:api public method=POST path=/bills/:bill_id/close
func (s *Service) CloseBill(ctx context.Context, bill_id string) (*CloseBillResponse, error) {
	bill, alreadyClosed, err := closeBill(ctx, bill_id)
	if err != nil {
		return nil, err
	}
	if !alreadyClosed && bill.WorkflowID != "" {
		if err := s.temporal.SignalWorkflow(ctx, bill.WorkflowID, "", closeSignalName, CloseSignal{Reason: "manual"}); err != nil {
			log.Printf("fees: failed to signal workflow %s: %v", bill.WorkflowID, err)
		}
	}
	return &CloseBillResponse{Bill: bill}, nil
}

//encore:api public method=GET path=/bills/:bill_id
func GetBill(ctx context.Context, bill_id string) (*Bill, error) {
	bill, err := fetchBill(ctx, bill_id)
	if err != nil {
		return nil, err
	}
	return bill, nil
}

type ListBillsParams struct {
	Status string `query:"status"`
}

type ListBillsResponse struct {
	Bills []Bill `json:"bills"`
}

//encore:api public method=GET path=/bills
func ListBills(ctx context.Context, p *ListBillsParams) (*ListBillsResponse, error) {
	query := `
		SELECT id, status, period_start, period_end, closed_at, charged_at,
			   totals_by_currency, line_item_count, metadata, workflow_id, created_at, updated_at
		FROM bills
	`
	var args []interface{}
	if p != nil && p.Status != "" {
		status := BillStatus(p.Status)
		if status != BillOpen && status != BillClosed && status != BillCharged {
			return nil, badRequest("invalid status")
		}
		query += " WHERE status = $1"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC"

	rows, err := billsDB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bills []Bill
	for rows.Next() {
		var bill Bill
		var totalsRaw []byte
		if err := rows.Scan(
			&bill.ID,
			&bill.Status,
			&bill.PeriodStart,
			&bill.PeriodEnd,
			&bill.ClosedAt,
			&bill.ChargedAt,
			&totalsRaw,
			&bill.LineItemCount,
			&bill.Metadata,
			&bill.WorkflowID,
			&bill.CreatedAt,
			&bill.UpdatedAt,
		); err != nil {
			return nil, err
		}
		bill.TotalsByCurrency = map[string]int64{}
		if len(totalsRaw) > 0 {
			if err := json.Unmarshal(totalsRaw, &bill.TotalsByCurrency); err != nil {
				return nil, err
			}
		}
		bills = append(bills, bill)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &ListBillsResponse{Bills: bills}, nil
}

func fetchBill(ctx context.Context, billID string) (*Bill, error) {
	var bill Bill
	var totalsRaw []byte
	err := billsDB.QueryRow(ctx, `
		SELECT id, status, period_start, period_end, closed_at, charged_at,
			   totals_by_currency, line_item_count, metadata, workflow_id, created_at, updated_at
		FROM bills
		WHERE id = $1
	`, billID).Scan(
		&bill.ID,
		&bill.Status,
		&bill.PeriodStart,
		&bill.PeriodEnd,
		&bill.ClosedAt,
		&bill.ChargedAt,
		&totalsRaw,
		&bill.LineItemCount,
		&bill.Metadata,
		&bill.WorkflowID,
		&bill.CreatedAt,
		&bill.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sqldb.ErrNoRows) {
			return nil, notFound("bill not found")
		}
		return nil, err
	}
	bill.TotalsByCurrency = map[string]int64{}
	if len(totalsRaw) > 0 {
		if err := json.Unmarshal(totalsRaw, &bill.TotalsByCurrency); err != nil {
			return nil, err
		}
	}
	return &bill, nil
}

func closeBill(ctx context.Context, billID string) (Bill, bool, error) {
	tx, err := billsDB.Begin(ctx)
	if err != nil {
		return Bill{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var bill Bill
	var totalsRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT id, status, period_start, period_end, closed_at, charged_at,
			   totals_by_currency, line_item_count, metadata, workflow_id, created_at, updated_at
		FROM bills
		WHERE id = $1
		FOR UPDATE
	`, billID).Scan(
		&bill.ID,
		&bill.Status,
		&bill.PeriodStart,
		&bill.PeriodEnd,
		&bill.ClosedAt,
		&bill.ChargedAt,
		&totalsRaw,
		&bill.LineItemCount,
		&bill.Metadata,
		&bill.WorkflowID,
		&bill.CreatedAt,
		&bill.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sqldb.ErrNoRows) {
			return Bill{}, false, notFound("bill not found")
		}
		return Bill{}, false, err
	}

	bill.TotalsByCurrency = map[string]int64{}
	if len(totalsRaw) > 0 {
		if err := json.Unmarshal(totalsRaw, &bill.TotalsByCurrency); err != nil {
			return Bill{}, false, err
		}
	}

	if bill.Status == BillClosed || bill.Status == BillCharged {
		if err := tx.Commit(); err != nil {
			return Bill{}, false, err
		}
		return bill, true, nil
	}

	now := time.Now().UTC()
	bill.Status = BillClosed
	bill.ClosedAt = &now
	bill.UpdatedAt = now

	_, err = tx.Exec(ctx, `
		UPDATE bills
		SET status = $1,
			closed_at = $2,
			updated_at = $3
		WHERE id = $4
	`, bill.Status, bill.ClosedAt, bill.UpdatedAt, bill.ID)
	if err != nil {
		return Bill{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Bill{}, false, err
	}
	return bill, false, nil
}

func validateCreateInput(p BillingWorkflowInput) error {
	if p.BillID == "" {
		return errors.New("bill id is required")
	}
	if p.WorkflowID == "" {
		return errors.New("workflow id is required")
	}
	if p.PeriodEnd.Before(p.PeriodStart) {
		return errors.New("period end before period start")
	}
	return nil
}

func insertBillRecord(ctx context.Context, input BillingWorkflowInput) error {
	if err := validateCreateInput(input); err != nil {
		return err
	}

	now := time.Now().UTC()
	tx, err := billsDB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(ctx, `
		INSERT INTO bills
			(id, status, period_start, period_end, totals_by_currency,
			 line_item_count, metadata, workflow_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO NOTHING
	`, input.BillID, BillOpen, input.PeriodStart, input.PeriodEnd, []byte("{}"),
		0, input.Metadata, input.WorkflowID, now, now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func badRequest(msg string) error {
	return errs.B().Code(errs.InvalidArgument).Msg(msg).Err()
}

func notFound(msg string) error {
	return errs.B().Code(errs.NotFound).Msg(msg).Err()
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexStr[0:8],
		hexStr[8:12],
		hexStr[12:16],
		hexStr[16:20],
		hexStr[20:32],
	), nil
}
