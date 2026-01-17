ALTER TABLE bills ADD COLUMN idempotency_key TEXT NULL;
CREATE UNIQUE INDEX bills_idempotency_key_idx ON bills(idempotency_key) WHERE idempotency_key IS NOT NULL;
