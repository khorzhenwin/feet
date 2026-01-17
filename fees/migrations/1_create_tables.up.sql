CREATE TABLE bills (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ NULL,
    charged_at TIMESTAMPTZ NULL,
    totals_by_currency JSONB NOT NULL DEFAULT '{}'::jsonb,
    line_item_count INT NOT NULL DEFAULT 0,
    metadata JSONB NULL,
    workflow_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE transaction_records (
    id UUID PRIMARY KEY,
    bill_id UUID NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
    description TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency TEXT NOT NULL,
    metadata JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX transaction_records_bill_id_idx ON transaction_records(bill_id);
