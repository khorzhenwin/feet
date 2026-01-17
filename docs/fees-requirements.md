# Fees Service Requirements

## Goal
Build a Fees API that manages bills and transaction records, accrues fees during a billing period, and finalizes totals at period end. A Temporal workflow starts at the beginning of a fee period and closes the bill after one month unless manually closed earlier.

## Scope
- Bill lifecycle management (open → closed) or (open → charged).
- Line item (transaction record) management.
- Read access to open and closed bills.
- Temporal-driven billing period closure.
- Multi-currency line items with explicit currency aggregation on bills.

## Non-Goals
- Currency conversion or exchange rates.
- Payment processing or settlement integration.
- Multi-tenant auth/authorization (can be added later).

## Domain Model

### Bill
- **id**: unique identifier (UUID).
- **status**: `open | closed | charged`.
- **period_start**: timestamp.
- **period_end**: timestamp (scheduled for one month after start).
- **closed_at**: timestamp (nullable).
- **charged_at**: timestamp (nullable).
- **totals_by_currency**: map of currency → total amount in minor units.
- **line_item_count**: integer.
- **metadata**: optional JSON (future extensibility).

### TransactionRecord (Line Item)
- **id**: unique identifier (UUID).
- **bill_id**: foreign key to Bill.
- **description**: string.
- **amount_minor**: integer (minor units, e.g. cents).
- **currency**: `USD | GEL`.
- **created_at**: timestamp.
- **metadata**: optional JSON.

### Money Representation
- **amount_minor** is an integer in minor units to avoid floating point errors.
- **currency** is an enum: `USD`, `GEL`.
- Bill totals are stored as a per-currency map (no conversion).

## Bill Lifecycle

1. **Open**
   - Bill is created with `status=open`.
   - Line items can be added.
   - Totals are incrementally updated per currency as line items are added.

2. **Closed**
   - Bill is closed either:
     - Automatically by Temporal after one month.
     - Manually by API call.
   - No new line items are accepted.
   - Closing produces a deterministic snapshot of totals and line items.

3. **Charged**
   - Optional final status after settlement.
   - Captures the final amount charged (mirrors totals_by_currency).

## Temporal Workflow

### Workflow Start
- Triggered when a new billing period starts.
- Inputs:
  - `bill_id`
  - `period_start`
  - `period_end` (period_start + 1 month)

### Activities
1. **Create bill record**
   - Inserts bill with `status=open`, period boundaries.
2. **Sleep until period_end**
   - Uses Temporal `Workflow.sleep(1 month)` or computed duration to `period_end`.
3. **Close bill**
   - Calls the API endpoint to close the bill.

### Signals
- **Cancel/Close Signal**
  - When a bill is manually closed, signal the workflow to end early.
  - Workflow should confirm and exit without re-closing.

### Idempotency
- Bill creation and close operations must be idempotent.
- Temporal activities should tolerate retries.

## API Semantics (Proposed REST)

### Create Bill
- `POST /bills`
- Request:
  - `period_start` (optional, default now)
  - `period_end` (optional; if absent, calculated as +1 month)
  - `metadata` (optional)
- Response:
  - `bill` object
  - `workflow_id`

### Add Line Item
- `POST /bills/:bill_id/line-items`
- Request:
  - `description`
  - `amount_minor`
  - `currency`
  - `metadata` (optional)
- Validation:
  - Reject if bill status is not `open`.
- Response:
  - `line_item`
  - updated `totals_by_currency`

### Close Bill
- `POST /bills/:bill_id/close`
- Effect:
  - If open, mark closed and freeze totals.
  - If already closed, return current state (idempotent).
  - If charged, return error or no-op (policy to define).

### Charge Bill (Optional)
- `POST /bills/:bill_id/charge`
- Marks bill as charged and records `charged_at`.

### Get Bill
- `GET /bills/:bill_id`
- Response:
  - full bill with line items and totals.

### List Bills
- `GET /bills?status=open|closed|charged&from=...&to=...`
- Response:
  - list of bill summaries.

## Validation Rules
- Line items can only be added when bill status is `open`.
- Currency must be `USD` or `GEL`.
- Amount must be non-negative integer.
- Close and charge operations are idempotent.

## Testing Architecture

### Local Dependencies (Docker Compose)
- **Postgres** for persistent local state and e2e runs.
- **Temporal Server** for workflow timers and signals.
- **Temporal UI** (optional) for debugging workflows.

### Test Layers
1. **Unit Tests**
   - Pure logic: money math, totals by currency, and state transitions.
2. **Integration Tests**
   - Encore service + database validation.
   - Temporal interactions stubbed or mocked.
3. **E2E Tests (Option A: Temporal Server)**
   - Uses Docker Compose Temporal server.
   - Exercises real workflow timers, signals, and API calls end-to-end.

### E2E Scenarios
- Auto-close after period: create bill → add line items → wait → bill closed.
- Manual close: create bill → add items → close → signal workflow cancel → no re-close.
- Reject after close: add line item to closed bill → validation error.
- Multi-currency totals: USD + GEL items → totals map includes both currencies.

### Environment Wiring
- `TEMPORAL_ADDRESS` (default `localhost:7233`)
- `TEMPORAL_NAMESPACE` (default `default`)
- `FEE_PERIOD` (duration string, used in tests to shorten 1 month period)
- Example values in `config/env.example`

## Data Storage (SQL)

### bills
- `id` UUID PK
- `status` text
- `period_start` timestamptz
- `period_end` timestamptz
- `closed_at` timestamptz null
- `charged_at` timestamptz null
- `totals_by_currency` jsonb
- `line_item_count` int
- `metadata` jsonb
- `created_at` timestamptz
- `updated_at` timestamptz

### transaction_records
- `id` UUID PK
- `bill_id` UUID FK
- `description` text
- `amount_minor` bigint
- `currency` text
- `metadata` jsonb
- `created_at` timestamptz

## Cleanup Tasks (Post-Doc)
- Remove `hello` service and replace with `fees` service.
- Update `README.md` to describe the Fees API and Temporal workflow.
